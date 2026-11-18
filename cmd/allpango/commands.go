package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/CSGrandeur/allpango/internal/config"
	"github.com/CSGrandeur/allpango/internal/sync"
)

func runRemotes(args []string) error {
	fs := flag.NewFlagSet("remotes", flag.ContinueOnError)
	var cfgPath string
	fs.StringVar(&cfgPath, "c", "", "多网盘配置文件路径（JSON）")
	fs.StringVar(&cfgPath, "config", "", "多网盘配置文件路径（JSON）")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if cfgPath == "" {
		return fmt.Errorf("remotes 需要 -c/--config 配置文件")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	type item struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Path     string `json:"path,omitempty"`
		Manifest string `json:"manifest,omitempty"`
		Parallel int    `json:"parallel,omitempty"`
	}
	names := cfg.Names()
	items := make([]item, 0, len(names))
	for _, n := range names {
		rc := cfg.Remotes[n]
		items = append(items, item{
			Name:     n,
			Provider: rc.Provider,
			Path:     rc.Path,
			Manifest: rc.Manifest,
			Parallel: rc.Parallel,
		})
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(items)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "名称\t网盘\t默认目录\t清单")
	for _, it := range items {
		p := it.Path
		if p == "" {
			p = "-"
		}
		mf := it.Manifest
		if mf == "" {
			mf = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", it.Name, it.Provider, p, mf)
	}
	return w.Flush()
}

func runQuota(args []string) error {
	fs := flag.NewFlagSet("quota", flag.ContinueOnError)
	c := addCommonFlags(fs)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}
	t, err := c.resolveSingleTarget(false)
	if err != nil {
		return err
	}
	d, err := t.openDrive(logfFor(t.verbose))
	if err != nil {
		return err
	}
	q, err := d.Quota()
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
	path := fs.String("path", "", "云端目录路径")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}
	t, err := c.resolveSingleTarget(false)
	if err != nil {
		return err
	}
	remotePath := *path
	if remotePath == "" {
		remotePath = t.defaultPath
	}
	if remotePath == "" {
		return fmt.Errorf("ls 需要 --path 云端目录（或在配置文件中填写 path）")
	}
	d, err := t.openDrive(logfFor(t.verbose))
	if err != nil {
		return err
	}
	cleanRemote, err := d.CleanPath(remotePath)
	if err != nil {
		return err
	}
	entries, err := d.ListDir(cleanRemote)
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
		ts := "-"
		if mt > 0 {
			ts = time.Unix(mt, 0).Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", kind, e.Size, ts, e.ServerFilename)
	}
	return w.Flush()
}

func runUpload(args []string) error {
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	c := addCommonFlags(fs)
	local := fs.String("local", "", "本地文件路径")
	path := fs.String("path", "", "云端文件绝对路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *local == "" || *path == "" {
		return fmt.Errorf("upload 需要 --local 和 --path")
	}
	t, err := c.resolveSingleTarget(false)
	if err != nil {
		return err
	}
	d, err := t.openDrive(logfFor(t.verbose))
	if err != nil {
		return err
	}
	cleanRemote, err := d.CleanPath(*path)
	if err != nil {
		return err
	}
	res, err := d.UploadFile(*local, cleanRemote)
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

type syncPlan struct {
	target       target
	remoteRoot   string
	manifestPath string
	parallel     int
}

type multiSyncItem struct {
	Remote     string      `json:"remote"`
	Provider   string      `json:"provider"`
	RemoteRoot string      `json:"remote_root"`
	OK         bool        `json:"ok"`
	Error      string      `json:"error,omitempty"`
	Stats      *sync.Stats `json:"stats,omitempty"`
}

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	c := addCommonFlags(fs)
	local := fs.String("local", "", "本地备份目录")
	path := fs.String("path", "", "云端目标目录")
	manifest := fs.String("manifest", "", "清单文件路径；不传则不用清单")
	parallel := fs.Int("parallel", 4, "同时上传的文件数")
	seed := fs.Bool("seed", false, "先按云端列表给清单播种：大小与修改时间一致的文件不再重传")
	seedLegacy := fs.Bool("seed-legacy", false, "接管旧工具目录：只按大小对账播种")
	all := fs.Bool("all", false, "同步配置文件里的全部网盘")
	asJSON := fs.Bool("json", false, "结束时以 JSON 输出统计")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *local == "" {
		return fmt.Errorf("sync 需要 --local 本地目录")
	}
	if err := sync.EnsureLocalDir(*local); err != nil {
		return err
	}

	setFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	targets, err := c.resolveMultiTargets(*all)
	if err != nil {
		return err
	}
	if len(targets) > 1 {
		if setFlags["path"] {
			return fmt.Errorf("同时同步多个网盘时不能在命令行传 --path，请在配置文件各 remote 中分别设置 path")
		}
		if setFlags["manifest"] {
			return fmt.Errorf("同时同步多个网盘时不能在命令行传 --manifest，请在配置文件各 remote 中分别设置 manifest")
		}
	}

	plans := make([]syncPlan, 0, len(targets))
	for _, t := range targets {
		rp := *path
		if !setFlags["path"] {
			rp = t.defaultPath
		}
		if rp == "" {
			if t.remoteName != "" {
				return fmt.Errorf("remote %q 缺少远端目录 path（请在配置文件中设置）", t.remoteName)
			}
			return fmt.Errorf("sync 需要 --path 云端目录")
		}
		mf := *manifest
		if !setFlags["manifest"] {
			mf = t.defaultManifest
		}
		par := *parallel
		if !setFlags["parallel"] && t.defaultParallel > 0 {
			par = t.defaultParallel
		}
		plans = append(plans, syncPlan{
			target:       t,
			remoteRoot:   rp,
			manifestPath: mf,
			parallel:     par,
		})
	}

	if len(plans) == 1 {
		stats, err := execSyncOne(plans[0], *local, *seed, *seedLegacy, "")
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

	items := make([]multiSyncItem, 0, len(plans))
	var failures []string
	for _, pl := range plans {
		stats, err := execSyncOne(pl, *local, *seed, *seedLegacy, pl.target.remoteName)
		it := multiSyncItem{
			Remote:     pl.target.remoteName,
			Provider:   pl.target.provider,
			RemoteRoot: pl.remoteRoot,
			OK:         err == nil,
			Stats:      stats,
		}
		if err != nil {
			it.Error = err.Error()
			failures = append(failures, fmt.Sprintf("%s: %v", pl.target.remoteName, err))
			if !*asJSON {
				fmt.Fprintf(os.Stderr, "[%s] 同步失败: %v\n", pl.target.remoteName, err)
			}
		} else if !*asJSON {
			fmt.Printf("[%s] 同步完成：%d 个文件，上传 %d（%.2f MB，秒传 %d），清单跳过 %d，空文件跳过 %d，耗时 %.1f 秒\n",
				pl.target.remoteName, stats.TotalFiles, stats.Uploaded, float64(stats.UploadedBytes)/1024/1024,
				stats.Rapid, stats.Skipped, stats.EmptySkipped, stats.DurationSec)
		}
		items = append(items, it)
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Results []multiSyncItem `json:"results"`
		}{Results: items})
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d 个网盘同步失败：\n%s", len(failures), strings.Join(failures, "\n"))
	}
	return nil
}

func execSyncOne(pl syncPlan, localDir string, seed, seedLegacy bool, logPrefix string) (*sync.Stats, error) {
	baseLog := logfFor(true)
	if logPrefix != "" {
		baseLog = prefixLogf(logPrefix, baseLog)
	}
	d, err := pl.target.openDrive(baseLog)
	if err != nil {
		return nil, err
	}
	cleanRemote, err := d.CleanPath(pl.remoteRoot)
	if err != nil {
		return nil, err
	}
	opts := sync.Options{
		Provider:     d.Provider(),
		LocalRoot:    localDir,
		RemoteRoot:   cleanRemote,
		ManifestPath: pl.manifestPath,
		Parallel:     pl.parallel,
		Logf:         baseLog,
	}
	if seed || seedLegacy {
		var seeded int
		if seedLegacy {
			seeded, err = sync.SeedLegacy(d.Lister(), opts)
		} else {
			seeded, err = sync.Seed(d.Lister(), opts)
		}
		if err != nil {
			return nil, fmt.Errorf("清单播种失败: %w", err)
		}
		if logPrefix != "" {
			fmt.Fprintf(os.Stderr, "[%s] 清单播种：%d 个文件与云端一致，不再重传\n", logPrefix, seeded)
		} else {
			fmt.Fprintf(os.Stderr, "清单播种：%d 个文件与云端一致，不再重传\n", seeded)
		}
	}
	return sync.Run(d.Uploader(), opts)
}

type multiVerifyItem struct {
	Remote     string             `json:"remote"`
	Provider   string             `json:"provider"`
	RemoteRoot string             `json:"remote_root"`
	OK         bool               `json:"ok"`
	Error      string             `json:"error,omitempty"`
	Verify     *sync.VerifyResult `json:"verify,omitempty"`
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	c := addCommonFlags(fs)
	local := fs.String("local", "", "本地备份目录")
	path := fs.String("path", "", "云端目录")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	sizeOnly := fs.Bool("size-only", false, "只按大小核对（接管旧工具目录时用）")
	all := fs.Bool("all", false, "核对配置文件里的全部网盘")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *local == "" {
		return fmt.Errorf("verify 需要 --local 本地目录")
	}
	if err := sync.EnsureLocalDir(*local); err != nil {
		return err
	}

	setFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	targets, err := c.resolveMultiTargets(*all)
	if err != nil {
		return err
	}
	if len(targets) > 1 && setFlags["path"] {
		return fmt.Errorf("同时核对多个网盘时不能在命令行传 --path，请在配置文件各 remote 中分别设置 path")
	}

	plans := make([]syncPlan, 0, len(targets))
	for _, t := range targets {
		rp := *path
		if !setFlags["path"] {
			rp = t.defaultPath
		}
		if rp == "" {
			if t.remoteName != "" {
				return fmt.Errorf("remote %q 缺少远端目录 path（请在配置文件中设置）", t.remoteName)
			}
			return fmt.Errorf("verify 需要 --path 云端目录")
		}
		plans = append(plans, syncPlan{target: t, remoteRoot: rp})
	}

	if len(plans) == 1 {
		v, err := execVerifyOne(plans[0], *local, *sizeOnly, "")
		if *asJSON && v != nil {
			_ = json.NewEncoder(os.Stdout).Encode(v)
		}
		if err != nil {
			if v == nil {
				return err
			}
		}
		if !*asJSON && v != nil {
			printVerifySummary("", v)
		}
		return err
	}

	items := make([]multiVerifyItem, 0, len(plans))
	var failures []string
	for _, pl := range plans {
		v, err := execVerifyOne(pl, *local, *sizeOnly, pl.target.remoteName)
		it := multiVerifyItem{
			Remote:     pl.target.remoteName,
			Provider:   pl.target.provider,
			RemoteRoot: pl.remoteRoot,
			OK:         err == nil,
			Verify:     v,
		}
		if v != nil && !*asJSON {
			printVerifySummary(pl.target.remoteName, v)
		}
		if err != nil {
			it.Error = err.Error()
			failures = append(failures, fmt.Sprintf("%s: %v", pl.target.remoteName, err))
			if v == nil && !*asJSON {
				fmt.Fprintf(os.Stderr, "[%s] 核对失败: %v\n", pl.target.remoteName, err)
			}
		}
		items = append(items, it)
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Results []multiVerifyItem `json:"results"`
		}{Results: items})
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d 个网盘核对未通过：\n%s", len(failures), strings.Join(failures, "\n"))
	}
	return nil
}

func execVerifyOne(pl syncPlan, localDir string, sizeOnly bool, logPrefix string) (*sync.VerifyResult, error) {
	baseLog := logfFor(pl.target.verbose)
	if logPrefix != "" {
		baseLog = prefixLogf(logPrefix, baseLog)
	}
	d, err := pl.target.openDrive(baseLog)
	if err != nil {
		return nil, err
	}
	cleanRemote, err := d.CleanPath(pl.remoteRoot)
	if err != nil {
		return nil, err
	}
	v, err := sync.Verify(d.Lister(), sync.Options{
		Provider:   d.Provider(),
		LocalRoot:  localDir,
		RemoteRoot: cleanRemote,
	})
	if err != nil {
		return nil, err
	}
	if sizeOnly && len(v.SizeMismatch) == 0 && len(v.Missing) == 0 {
		if len(v.MtimeMismatch) > 0 {
			fmt.Fprintf(os.Stderr, "按大小核对通过（%d 个文件时间戳不同，忽略）\n", len(v.MtimeMismatch))
		}
		return v, nil
	}
	if !v.OK() {
		return v, fmt.Errorf("核对未通过：%d 个大小不符，%d 个时间不符，%d 个缺失", len(v.SizeMismatch), len(v.MtimeMismatch), len(v.Missing))
	}
	return v, nil
}

func printVerifySummary(prefix string, v *sync.VerifyResult) {
	head := ""
	if prefix != "" {
		head = "[" + prefix + "] "
	}
	fmt.Printf("%s核对完成：本地 %d 个文件（%.2f GB），一致 %d，大小不符 %d，时间不符 %d，云端缺失 %d，空文件 %d，云端多出 %d，耗时 %.1f 秒\n",
		head, v.LocalFiles, float64(v.LocalBytes)/1e9, v.Matched, len(v.SizeMismatch), len(v.MtimeMismatch), len(v.Missing), v.EmptySkipped, v.RemoteExtra, v.DurationSec)
	for _, s := range v.SizeMismatch {
		fmt.Println("  不符:", s)
	}
	for _, s := range v.Missing {
		fmt.Println("  缺失:", s)
	}
}
