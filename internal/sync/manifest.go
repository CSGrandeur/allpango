// Package sync 把本地备份目录同步到云端：只上传新增或变化的文件，
// 用本地清单记住「已确认上传成功」的状态，避免重复询问网盘。
// 清单只是加速：丢了就退回逐文件预上传，不能凭清单断定云端一定有。
package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ManifestEntry 记录一个已确认上传成功的本地文件状态。
type ManifestEntry struct {
	Size       int64  `json:"size"`
	Mtime      int64  `json:"mtime"` // 本地修改时间（Unix 秒）
	MD5        string `json:"md5"`
	UploadedAt int64  `json:"uploaded_at"`
	RemotePath string `json:"remote_path"`
}

// Manifest 是清单文件：相对路径 → 状态。
type Manifest struct {
	Version int                      `json:"version"`
	Files   map[string]ManifestEntry `json:"files"`
}

// LoadManifest 读清单。文件不存在或损坏都返回空清单（退回全量核对），不报错。
func LoadManifest(path string) *Manifest {
	m := &Manifest{Version: 1, Files: map[string]ManifestEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	var loaded Manifest
	if err := json.Unmarshal(data, &loaded); err != nil {
		return m
	}
	if loaded.Files != nil {
		m.Files = loaded.Files
	}
	return m
}

// Save 原子写回清单。
func (m *Manifest) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".allpango-manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Unchanged 判断本地文件相对清单是否未变（大小与修改时间都一致）。
func (m *Manifest) Unchanged(rel string, size, mtime int64) bool {
	e, ok := m.Files[rel]
	return ok && e.Size == size && e.Mtime == mtime
}

// Mark 记录一次成功上传。
func (m *Manifest) Mark(rel string, size, mtime int64, md5, remotePath string) {
	m.Files[rel] = ManifestEntry{
		Size: size, Mtime: mtime, MD5: md5,
		UploadedAt: time.Now().Unix(), RemotePath: remotePath,
	}
}

// LocalFile 是本地扫描到的一個文件。
type LocalFile struct {
	Rel   string // 相对同步根目录，/ 分隔
	Abs   string
	Size  int64
	Mtime int64
}

// WalkLocal 扫描本地目录，按相对路径排序返回全部文件。
// 不跟随符号链接；忽略清单文件自身。
// 无权限的目录/文件记入 skipped 并继续（备份目录里可能有其他服务留下的受限目录），
// 由调用方把 skipped 报告给用户；不因个别目录不可读而中断整个备份。
func WalkLocal(root, manifestPath string) ([]LocalFile, []string, error) {
	var files []LocalFile
	var skipped []string
	manifestAbs, _ := filepath.Abs(manifestPath)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				skipped = append(skipped, path)
				if info != nil && info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		// Walk 用 Lstat，符号链接本身是 ModeSymlink，不会跟随。
		if !info.Mode().IsRegular() {
			return nil
		}
		abs, _ := filepath.Abs(path)
		if abs == manifestAbs {
			return nil
		}
		// allpango 自身的状态文件（清单等）不属于备份内容
		if strings.HasPrefix(info.Name(), ".allpango_") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, LocalFile{
			Rel: filepath.ToSlash(rel), Abs: path,
			Size: info.Size(), Mtime: info.ModTime().Unix(),
		})
		return nil
	})
	if err != nil {
		return nil, skipped, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return files, skipped, nil
}

// JoinRemote 拼云端路径：remoteRoot + / + rel。
func JoinRemote(remoteRoot, rel string) string {
	return strings.TrimRight(remoteRoot, "/") + "/" + rel
}
