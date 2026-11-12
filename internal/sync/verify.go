package sync

import (
	"fmt"
	"strings"
	"time"

	"github.com/CSGrandeur/allpango/internal/api"
)

// Lister 是核对与播种需要的云端列表能力（*api.Client 满足）。
type Lister interface {
	ListAll(dir string) ([]api.Entry, error)
}

// VerifyResult 是一次核对的结果。
type VerifyResult struct {
	LocalFiles    int      `json:"local_files"`
	LocalBytes    int64    `json:"local_bytes"`
	Matched       int      `json:"matched"`        // 大小与修改时间都一致
	SizeMismatch  []string `json:"size_mismatch"`  // 云端有但大小不同
	MtimeMismatch []string `json:"mtime_mismatch"` // 大小一致但修改时间不同（旧工具时间戳语义差异）
	Missing       []string `json:"missing"`        // 云端没有
	EmptySkipped  int      `json:"empty_skipped"`  // 空文件，官方不收，不计缺失
	RemoteExtra   int      `json:"remote_extra"`   // 云端多出来的文件（只报告，不删除）
	Unreadable    []string `json:"unreadable"`     // 无权限跳过的本地路径
	DurationSec   float64  `json:"duration_sec"`
}

// OK 表示本地每个非空文件在云端都有且大小、修改时间一致。
func (v *VerifyResult) OK() bool {
	return len(v.SizeMismatch) == 0 && len(v.Missing) == 0 && len(v.MtimeMismatch) == 0
}

// remoteIndex 把云端递归列表按「相对路径」建索引。
func remoteIndex(entries []api.Entry, remoteRoot string) map[string]api.Entry {
	prefix := strings.TrimRight(remoteRoot, "/") + "/"
	idx := make(map[string]api.Entry, len(entries))
	for _, e := range entries {
		if e.IsDir == 1 {
			continue
		}
		if strings.HasPrefix(e.Path, prefix) {
			idx[strings.TrimPrefix(e.Path, prefix)] = e
		}
	}
	return idx
}

// Verify 逐文件核对本地目录与云端：大小 + local_mtime（上传时写入的本地修改时间）。
// 不依赖清单，直接问网盘，用于同步后的独立核验。
func Verify(lister Lister, opts Options) (*VerifyResult, error) {
	if opts.LocalRoot == "" || opts.RemoteRoot == "" {
		return nil, fmt.Errorf("核对需要本地目录和云端目录")
	}
	start := time.Now()
	files, skipped, err := WalkLocal(opts.LocalRoot, opts.ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("扫描本地目录失败: %w", err)
	}
	entries, err := lister.ListAll(opts.RemoteRoot)
	if err != nil {
		return nil, fmt.Errorf("递归列云端目录失败: %w", err)
	}
	idx := remoteIndex(entries, opts.RemoteRoot)

	v := &VerifyResult{LocalFiles: len(files), SizeMismatch: []string{}, MtimeMismatch: []string{}, Missing: []string{}, Unreadable: skipped}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		v.LocalBytes += f.Size
		if f.Size == 0 {
			v.EmptySkipped++
			continue
		}
		seen[f.Rel] = true
		e, ok := idx[f.Rel]
		if !ok {
			v.Missing = append(v.Missing, f.Rel)
			continue
		}
		if e.Size != f.Size {
			v.SizeMismatch = append(v.SizeMismatch, fmt.Sprintf("%s: 本地 %d ≠ 云端 %d", f.Rel, f.Size, e.Size))
			continue
		}
		// local_mtime 是上传方写入的本地修改时间；旧工具（不含该语义）传的文件可能为 0，
		// 这时只按大小核对，不算失败。
		if e.LocalMtime != 0 && e.LocalMtime != f.Mtime {
			v.MtimeMismatch = append(v.MtimeMismatch, fmt.Sprintf("%s: 本地 %d ≠ 云端 %d", f.Rel, f.Mtime, e.LocalMtime))
			continue
		}
		v.Matched++
	}
	for rel := range idx {
		if !seen[rel] {
			v.RemoteExtra++
		}
	}
	v.DurationSec = time.Since(start).Seconds()
	return v, nil
}

// Seed 用云端列表给清单播种：本地文件在云端存在且大小、修改时间一致的，记为已上传。
// 用于接管旧工具传了一半的目录：一致的不再重传，缺的、变了的照常上传。
// 返回播种条数。清单随后由 Run 正常使用与写回。
func Seed(lister Lister, opts Options) (int, error) {
	if opts.ManifestPath == "" {
		return 0, fmt.Errorf("播种需要 --manifest 清单文件")
	}
	files, _, err := WalkLocal(opts.LocalRoot, opts.ManifestPath)
	if err != nil {
		return 0, fmt.Errorf("扫描本地目录失败: %w", err)
	}
	entries, err := lister.ListAll(opts.RemoteRoot)
	if err != nil {
		return 0, fmt.Errorf("递归列云端目录失败: %w", err)
	}
	idx := remoteIndex(entries, opts.RemoteRoot)
	manifest := LoadManifest(opts.ManifestPath)
	seeded := 0
	for _, f := range files {
		if f.Size == 0 {
			continue
		}
		if manifest.Unchanged(f.Rel, f.Size, f.Mtime) {
			continue
		}
		e, ok := idx[f.Rel]
		if !ok || e.Size != f.Size {
			continue
		}
		if e.LocalMtime != 0 && e.LocalMtime != f.Mtime {
			continue
		}
		manifest.Mark(f.Rel, f.Size, f.Mtime, "", JoinRemote(opts.RemoteRoot, f.Rel))
		seeded++
	}
	if seeded > 0 {
		if err := manifest.Save(opts.ManifestPath); err != nil {
			return 0, fmt.Errorf("写清单失败: %w", err)
		}
	}
	return seeded, nil
}

// SeedLegacy 接管旧上传工具的目录：只按大小对账。
// 旧工具写入的 local_mtime 是它自己的上传时刻，与本地文件 mtime 对不上，
// 严格播种会全部落空导致整目录重传。备份文件按日期命名、生成后不再修改，
// 大小一致即可认定已传；此后清单记住本地 mtime，增量同步恢复严格判定。
// 内容一致性由随后的 Verify（同样按大小）与每日新文件的严格流程保证。
func SeedLegacy(lister Lister, opts Options) (int, error) {
	if opts.ManifestPath == "" {
		return 0, fmt.Errorf("播种需要 --manifest 清单文件")
	}
	files, _, err := WalkLocal(opts.LocalRoot, opts.ManifestPath)
	if err != nil {
		return 0, fmt.Errorf("扫描本地目录失败: %w", err)
	}
	entries, err := lister.ListAll(opts.RemoteRoot)
	if err != nil {
		return 0, fmt.Errorf("递归列云端目录失败: %w", err)
	}
	idx := remoteIndex(entries, opts.RemoteRoot)
	manifest := LoadManifest(opts.ManifestPath)
	seeded := 0
	for _, f := range files {
		if f.Size == 0 {
			continue
		}
		if manifest.Unchanged(f.Rel, f.Size, f.Mtime) {
			continue
		}
		e, ok := idx[f.Rel]
		if !ok || e.Size != f.Size {
			continue
		}
		manifest.Mark(f.Rel, f.Size, f.Mtime, "", JoinRemote(opts.RemoteRoot, f.Rel))
		seeded++
	}
	if seeded > 0 {
		if err := manifest.Save(opts.ManifestPath); err != nil {
			return 0, fmt.Errorf("写清单失败: %w", err)
		}
	}
	return seeded, nil
}
