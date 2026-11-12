package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/CSGrandeur/allpango/internal/api"
	"github.com/CSGrandeur/allpango/internal/sync"
)

func runQuota(args []string) error {
	fs := flag.NewFlagSet("quota", flag.ContinueOnError)
	c := addCommonFlags(fs)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := c.setup(true)
	if err != nil {
		return err
	}
	var q *api.Quota
	err = m.Do(func() error {
		var e error
		q, e = m.Client.GetQuota()
		return e
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(q)
	}
	fmt.Printf("总容量: %.2f GB\n已用:   %.2f GB\n剩余:   %.2f GB\n",
		float64(q.Total)/1e9, float64(q.Used)/1e9, float64(q.Total-q.Used)/1e9)
	return nil
}

func runLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	c := addCommonFlags(fs)
	path := fs.String("path", "", "云端目录（应用目录内的绝对路径）")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return fmt.Errorf("ls 需要 --path 云端目录")
	}
	m, err := c.setup(true)
	if err != nil {
		return err
	}
	var entries []api.Entry
	err = m.Do(func() error {
		var e error
		entries, e = m.Client.ListDir(*path)
		return e
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(entries)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "类型\t大小\t修改时间\t名称")
	for _, e := range entries {
		kind := "文件"
		if e.IsDir == 1 {
			kind = "目录"
		}
		mt := e.ServerMtime
		if mt == 0 {
			mt = e.LocalMtime
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", kind, e.Size,
			time.Unix(mt, 0).Format("2006-01-02 15:04:05"), e.ServerFilename)
	}
	return w.Flush()
}

func runUpload(args []string) error {
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	c := addCommonFlags(fs)
	local := fs.String("local", "", "本地文件路径")
	path := fs.String("path", "", "云端文件绝对路径（应用目录内）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *local == "" || *path == "" {
		return fmt.Errorf("upload 需要 --local 和 --path")
	}
	m, err := c.setup(true)
	if err != nil {
		return err
	}
	var res *api.UploadResult
	err = m.Do(func() error {
		var e error
		res, e = m.Client.UploadFile(*local, *path)
		return e
	})
	if err != nil {
		return err
	}
	tag := "上传"
	if res.Rapid {
		tag = "秒传"
	}
	fmt.Printf("%s完成: %s（%d 字节，分片 %d/%d，MD5 %s）\n",
		tag, res.Path, res.Size, res.ChunksUploaded, res.ChunksTotal, res.MD5)
	return nil
}

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	c := addCommonFlags(fs)
	local := fs.String("local", "", "本地备份目录")
	path := fs.String("path", "", "云端目标目录（应用目录内的绝对路径）")
	manifest := fs.String("manifest", "", "清单文件路径；不传则不用清单")
	parallel := fs.Int("parallel", 4, "同时上传的文件数")
	seed := fs.Bool("seed", false, "先按云端列表给清单播种：大小与修改时间一致的文件不再重传")
	seedLegacy := fs.Bool("seed-legacy", false, "接管旧工具目录：只按大小对账播种（旧工具的 local_mtime 不可比）")
	asJSON := fs.Bool("json", false, "结束时以 JSON 输出统计")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *local == "" || *path == "" {
		return fmt.Errorf("sync 需要 --local 和 --path")
	}
	if err := sync.EnsureLocalDir(*local); err != nil {
		return err
	}
	m, err := c.setup(true)
	if err != nil {
		return err
	}
	opts := sync.Options{
		LocalRoot: *local, RemoteRoot: *path, ManifestPath: *manifest,
		Parallel: *parallel, Logf: logfFor(true),
	}
	if *seed || *seedLegacy {
		var seeded int
		err := m.Do(func() error {
			var e error
			if *seedLegacy {
				seeded, e = sync.SeedLegacy(m.Client, opts)
			} else {
				seeded, e = sync.Seed(m.Client, opts)
			}
			return e
		})
		if err != nil {
			return fmt.Errorf("清单播种失败: %w", err)
		}
		fmt.Fprintf(os.Stderr, "清单播种：%d 个文件与云端一致，不再重传\n", seeded)
	}
	stats, err := sync.Run(sync.ManagedUploader{Client: m.Client, Do: m.Do}, opts)
	if *asJSON && stats != nil {
		_ = json.NewEncoder(os.Stdout).Encode(stats)
	}
	if err != nil {
		return err
	}
	if !*asJSON {
		fmt.Printf("同步完成：%d 个文件，上传 %d（%.2f MB，秒传 %d），清单跳过 %d，空文件跳过 %d，耗时 %.1f 秒\n",
			stats.TotalFiles, stats.Uploaded, float64(stats.UploadedBytes)/1024/1024,
			stats.Rapid, stats.Skipped, stats.EmptySkipped, stats.DurationSec)
	}
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	c := addCommonFlags(fs)
	local := fs.String("local", "", "本地备份目录")
	path := fs.String("path", "", "云端目录（应用目录内的绝对路径）")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	sizeOnly := fs.Bool("size-only", false, "只按大小核对（接管旧工具目录时用，其时间戳语义不同）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *local == "" || *path == "" {
		return fmt.Errorf("verify 需要 --local 和 --path")
	}
	if err := sync.EnsureLocalDir(*local); err != nil {
		return err
	}
	m, err := c.setup(true)
	if err != nil {
		return err
	}
	var v *sync.VerifyResult
	err = m.Do(func() error {
		var e error
		v, e = sync.Verify(m.Client, sync.Options{LocalRoot: *local, RemoteRoot: *path})
		return e
	})
	if err != nil {
		return err
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(v)
	} else {
		fmt.Printf("核对完成：本地 %d 个文件（%.2f GB），一致 %d，大小不符 %d，时间不符 %d，云端缺失 %d，空文件 %d，云端多出 %d，耗时 %.1f 秒\n",
			v.LocalFiles, float64(v.LocalBytes)/1e9, v.Matched, len(v.SizeMismatch), len(v.MtimeMismatch), len(v.Missing), v.EmptySkipped, v.RemoteExtra, v.DurationSec)
		for _, s := range v.SizeMismatch {
			fmt.Println("  不符:", s)
		}
		for _, s := range v.Missing {
			fmt.Println("  缺失:", s)
		}
	}
	if *sizeOnly && len(v.SizeMismatch) == 0 && len(v.Missing) == 0 {
		if len(v.MtimeMismatch) > 0 {
			fmt.Fprintf(os.Stderr, "按大小核对通过（%d 个文件时间戳不同，旧工具语义差异，不计失败）\n", len(v.MtimeMismatch))
		}
		return nil
	}
	if !v.OK() {
		return fmt.Errorf("核对未通过：%d 个大小不符，%d 个时间不符，%d 个缺失", len(v.SizeMismatch), len(v.MtimeMismatch), len(v.Missing))
	}
	return nil
}
