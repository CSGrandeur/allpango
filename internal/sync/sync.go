package sync

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/CSGrandeur/allpango/internal/api"
)

// Uploader 是同步需要的上传能力。
type Uploader interface {
	UploadFile(localPath, remotePath string) (*api.UploadResult, error)
}

// ManagedUploader 把上传包进令牌管理：令牌失效时自动刷新并重试一次。
// *auth.Manager 的 Do 负责刷新逻辑。
type ManagedUploader struct {
	Client *api.Client
	Do     func(op func() error) error
}

func (u ManagedUploader) UploadFile(localPath, remotePath string) (*api.UploadResult, error) {
	var res *api.UploadResult
	err := u.Do(func() error {
		var e error
		res, e = u.Client.UploadFile(localPath, remotePath)
		return e
	})
	return res, err
}

// Options 控制一次同步。
type Options struct {
	LocalRoot    string // 本地备份目录
	RemoteRoot   string // 云端目标目录（应用目录内的绝对路径）
	ManifestPath string // 清单文件；空字符串表示不用清单
	Parallel     int    // 同时上传的文件数，默认 4
	Logf         func(string, ...any)
}

// Stats 是一次同步的统计。
type Stats struct {
	TotalFiles    int      `json:"total_files"`
	TotalBytes    int64    `json:"total_bytes"`
	Uploaded      int      `json:"uploaded"`
	UploadedBytes int64    `json:"uploaded_bytes"`
	Rapid         int      `json:"rapid"`
	Skipped       int      `json:"skipped"`       // 清单判定未变化
	EmptySkipped  int      `json:"empty_skipped"` // 空文件，官方不允许上传
	Failed        int      `json:"failed"`
	Unreadable    []string `json:"unreadable"` // 无权限跳过的本地路径
	DurationSec   float64  `json:"duration_sec"`
}

type job struct {
	file LocalFile
}

type outcome struct {
	file LocalFile
	res  *api.UploadResult
	err  error
}

// Run 执行同步：扫描本地 → 清单过滤 → 并发上传 → 写回清单。
// 任一文件失败则返回错误（退出码非 0），但其余文件继续传完。
func Run(up Uploader, opts Options) (*Stats, error) {
	if opts.LocalRoot == "" || opts.RemoteRoot == "" {
		return nil, fmt.Errorf("同步需要本地目录和云端目录")
	}
	if !strings.HasPrefix(opts.RemoteRoot, "/") {
		return nil, fmt.Errorf("云端目录必须是绝对路径: %s", opts.RemoteRoot)
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	parallel := opts.Parallel
	if parallel <= 0 {
		parallel = 4
	}

	start := time.Now()
	files, skipped, err := WalkLocal(opts.LocalRoot, opts.ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("扫描本地目录失败: %w", err)
	}
	stats := &Stats{TotalFiles: len(files)}
	for _, p := range skipped {
		logf("跳过无权限路径 %s", p)
	}
	stats.Unreadable = skipped
	for _, f := range files {
		stats.TotalBytes += f.Size
	}

	var manifest *Manifest
	if opts.ManifestPath != "" {
		manifest = LoadManifest(opts.ManifestPath)
	}

	var jobs []job
	for _, f := range files {
		if f.Size == 0 {
			stats.EmptySkipped++
			logf("跳过空文件 %s（官方接口不允许上传空文件）", f.Rel)
			continue
		}
		if manifest != nil && manifest.Unchanged(f.Rel, f.Size, f.Mtime) {
			stats.Skipped++
			continue
		}
		jobs = append(jobs, job{file: f})
	}
	logf("共 %d 个文件（%.2f MB）：待传 %d，清单跳过 %d，空文件跳过 %d",
		stats.TotalFiles, float64(stats.TotalBytes)/1024/1024, len(jobs), stats.Skipped, stats.EmptySkipped)

	// 并发上传，结果按完成顺序收集；清单写回在主协程。
	jobCh := make(chan job)
	resCh := make(chan outcome, len(jobs)+1)
	done := make(chan struct{})
	workers := parallel
	if workers > len(jobs) && len(jobs) > 0 {
		workers = len(jobs)
	}
	for i := 0; i < workers; i++ {
		go func() {
			for j := range jobCh {
				remote := JoinRemote(opts.RemoteRoot, j.file.Rel)
				res, err := up.UploadFile(j.file.Abs, remote)
				resCh <- outcome{file: j.file, res: res, err: err}
			}
			done <- struct{}{}
		}()
	}
	go func() {
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)
	}()
	go func() {
		for i := 0; i < workers; i++ {
			<-done
		}
		close(resCh)
	}()

	var failures []string
	for out := range resCh {
		if out.err != nil {
			if errors.Is(out.err, api.ErrEmptyFile) {
				stats.EmptySkipped++
				continue
			}
			stats.Failed++
			failures = append(failures, fmt.Sprintf("%s: %v", out.file.Rel, out.err))
			logf("上传失败 %s: %v", out.file.Rel, out.err)
			continue
		}
		stats.Uploaded++
		stats.UploadedBytes += out.file.Size
		if out.res.Rapid {
			stats.Rapid++
		}
		if manifest != nil {
			manifest.Mark(out.file.Rel, out.file.Size, out.file.Mtime, out.res.MD5, out.res.Path)
			// 每传完一个就写回，中断也不丢已完成的状态。
			if err := manifest.Save(opts.ManifestPath); err != nil {
				logf("写清单失败（不影响已上传文件）: %v", err)
			}
		}
		tag := "上传"
		if out.res.Rapid {
			tag = "秒传"
		}
		logf("%s完成 %s（%d 字节，分片 %d/%d）", tag, out.file.Rel, out.file.Size, out.res.ChunksUploaded, out.res.ChunksTotal)
	}

	stats.DurationSec = time.Since(start).Seconds()
	if len(failures) > 0 {
		sort.Strings(failures)
		return stats, fmt.Errorf("%d 个文件上传失败:\n%s", len(failures), strings.Join(failures, "\n"))
	}
	return stats, nil
}

// EnsureLocalDir 确认本地目录存在。
func EnsureLocalDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("本地目录不可用: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s 不是目录", dir)
	}
	return nil
}

// RemoteParent 返回云端路径的父目录。
func RemoteParent(p string) string {
	return path.Dir(p)
}
