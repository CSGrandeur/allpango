package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/CSGrandeur/allpango/internal/api"
	"github.com/CSGrandeur/allpango/internal/auth"
	"github.com/CSGrandeur/allpango/internal/config"
	"github.com/CSGrandeur/allpango/internal/sync"
	"github.com/CSGrandeur/allpango/internal/weiyun"
)

// commonFlags 是子命令共用的连接与认证参数。凭证、令牌与配置路径必须显式给出。
type commonFlags struct {
	configPath string
	remote     string
	provider   string
	cred       string
	token      string
	endpoint   string // 可选：接口地址覆盖（测试用），默认官方地址
	retryBase  string // 可选：重试退避基数（测试用），默认 1s/2s
	verbose    bool
}

func addCommonFlags(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.configPath, "c", "", "多网盘配置文件路径（JSON）")
	fs.StringVar(&c.configPath, "config", "", "多网盘配置文件路径（JSON）")
	fs.StringVar(&c.remote, "r", "", "配置中的网盘名称（多网盘可用逗号分隔）")
	fs.StringVar(&c.remote, "remote", "", "配置中的网盘名称（多网盘可用逗号分隔）")
	fs.StringVar(&c.provider, "p", "", "网盘类型（baidu / weiyun）")
	fs.StringVar(&c.provider, "provider", "", "网盘类型（baidu / weiyun）")
	fs.StringVar(&c.cred, "cred", "", "百度凭证文件路径（JSON：app_key / app_secret）")
	fs.StringVar(&c.token, "token", "", "令牌文件路径（权限 0600）")
	fs.StringVar(&c.endpoint, "endpoint", "", "接口地址覆盖，仅测试用；默认官方地址")
	fs.StringVar(&c.retryBase, "retry-base", "", "重试退避基数（如 10ms），仅测试用")
	fs.BoolVar(&c.verbose, "v", false, "输出进度日志")
	return c
}

func logfFor(verbose bool) func(string, ...any) {
	if !verbose {
		return func(string, ...any) {}
	}
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	}
}

func prefixLogf(prefix string, base func(string, ...any)) func(string, ...any) {
	if base == nil || prefix == "" {
		return base
	}
	return func(format string, args ...any) {
		base("[%s] %s", prefix, fmt.Sprintf(format, args...))
	}
}

type target struct {
	remoteName      string
	provider        string
	credPath        string
	tokenPath       string
	endpoint        string
	defaultPath     string
	defaultManifest string
	defaultParallel int
	retryBase       time.Duration
	hasRetryBase    bool
	verbose         bool
}

type drive interface {
	Provider() string
	CleanPath(p string) (string, error)
	Quota() (*api.Quota, error)
	ListDir(dir string) ([]api.Entry, error)
	UploadFile(localPath, remotePath string) (*api.UploadResult, error)
	Uploader() sync.Uploader
	Lister() sync.Lister
}

func (c *commonFlags) parseRetryBase() (time.Duration, bool, error) {
	if c.retryBase == "" {
		return 0, false, nil
	}
	d, err := time.ParseDuration(c.retryBase)
	if err != nil {
		return 0, false, fmt.Errorf("--retry-base 不合法: %w", err)
	}
	return d, true, nil
}

func (c *commonFlags) resolveSingleTarget(forLogin bool) (target, error) {
	targets, err := c.resolveTargets(false, forLogin)
	if err != nil {
		return target{}, err
	}
	if len(targets) != 1 {
		return target{}, fmt.Errorf("该命令只支持单个网盘，当前选中了 %d 个", len(targets))
	}
	return targets[0], nil
}

func (c *commonFlags) resolveMultiTargets(all bool) ([]target, error) {
	return c.resolveTargets(all, false)
}

func (c *commonFlags) resolveTargets(all, forLogin bool) ([]target, error) {
	rb, hasRB, err := c.parseRetryBase()
	if err != nil {
		return nil, err
	}
	if c.configPath != "" {
		if c.cred != "" || c.token != "" || c.provider != "" {
			return nil, fmt.Errorf("使用 --config 时不能同时指定 --cred / --token / --provider")
		}
		cfg, err := config.Load(c.configPath)
		if err != nil {
			return nil, err
		}
		selected, err := cfg.Select(c.remote, all)
		if err != nil {
			return nil, err
		}
		out := make([]target, 0, len(selected))
		for _, nr := range selected {
			ep := nr.Config.Endpoint
			if c.endpoint != "" {
				ep = c.endpoint
			}
			out = append(out, target{
				remoteName:      nr.Name,
				provider:        nr.Config.Provider,
				credPath:        nr.Config.Cred,
				tokenPath:       nr.Config.Token,
				endpoint:        ep,
				defaultPath:     nr.Config.Path,
				defaultManifest: nr.Config.Manifest,
				defaultParallel: nr.Config.Parallel,
				retryBase:       rb,
				hasRetryBase:    hasRB,
				verbose:         c.verbose,
			})
		}
		return out, nil
	}

	if c.remote != "" || all {
		return nil, fmt.Errorf("使用 -r 或 --all 需要配合 -c/--config 配置文件")
	}

	prov, err := c.detectDirectProvider(forLogin)
	if err != nil {
		return nil, err
	}
	return []target{{
		provider:     prov,
		credPath:     c.cred,
		tokenPath:    c.token,
		endpoint:     c.endpoint,
		retryBase:    rb,
		hasRetryBase: hasRB,
		verbose:      c.verbose,
	}}, nil
}

func (c *commonFlags) detectDirectProvider(forLogin bool) (string, error) {
	prov := strings.ToLower(strings.TrimSpace(c.provider))
	if prov != "" {
		switch prov {
		case config.ProviderBaidu:
			if c.cred == "" {
				return "", fmt.Errorf("缺少 --cred 凭证文件")
			}
			if c.token == "" {
				return "", fmt.Errorf("缺少 --token 令牌文件")
			}
			return config.ProviderBaidu, nil
		case config.ProviderWeiyun:
			if c.cred != "" {
				return "", fmt.Errorf("微云仅使用 --token 令牌文件，不需要 --cred")
			}
			if c.token == "" {
				return "", fmt.Errorf("缺少 --token 令牌文件")
			}
			return config.ProviderWeiyun, nil
		default:
			return "", fmt.Errorf("不支持的网盘类型 %q（可选: baidu, weiyun）", prov)
		}
	}

	if c.cred != "" {
		if c.token == "" {
			return "", fmt.Errorf("缺少 --token 令牌文件")
		}
		return config.ProviderBaidu, nil
	}
	if c.token == "" {
		return "", fmt.Errorf("缺少 --cred / --token，或使用 -c/--config 指定配置文件")
	}

	detected, err := config.DetectProviderFromTokenFile(c.token)
	if err != nil {
		return "", err
	}
	switch detected {
	case config.ProviderWeiyun:
		return config.ProviderWeiyun, nil
	case config.ProviderBaidu:
		return "", fmt.Errorf("检测到百度令牌文件，请补充 --cred 凭证文件")
	default:
		if forLogin {
			return "", fmt.Errorf("login 需要提供 --cred（百度）或指定 -p weiyun（微云）")
		}
		return "", fmt.Errorf("无法识别网盘类型，请传 --cred（百度）或确认微云令牌文件包含 mcp_token（也可指定 -p weiyun）")
	}
}

func (t target) openDrive(logf func(string, ...any)) (drive, error) {
	if logf == nil {
		logf = logfFor(t.verbose)
	}
	switch t.provider {
	case config.ProviderBaidu:
		cred, err := auth.LoadCredentials(t.credPath)
		if err != nil {
			return nil, err
		}
		ep := endpointsFor(t.endpoint)
		m, err := auth.NewManager(cred, t.tokenPath, ep, logf)
		if err != nil {
			return nil, err
		}
		if t.hasRetryBase {
			m.Client.RetryBase = t.retryBase
		}
		if !m.HasToken() {
			return nil, fmt.Errorf("还没有令牌，请先运行 allpango login")
		}
		return &baiduDrive{m: m}, nil
	case config.ProviderWeiyun:
		tf, err := weiyun.LoadTokenFile(t.tokenPath)
		if err != nil {
			return nil, err
		}
		if !tf.HasToken() {
			return nil, fmt.Errorf("还没有微云令牌，请先运行 allpango login -p weiyun --token %s", t.tokenPath)
		}
		cl := &weiyun.Client{
			MCPToken:   tf.MCPToken,
			EnvID:      tf.EnvID,
			MCPURL:     t.endpoint,
			MaxRetries: 3,
			Logf:       logf,
		}
		if t.hasRetryBase {
			cl.RetryBase = t.retryBase
		}
		return &weiyunDrive{c: cl}, nil
	default:
		return nil, fmt.Errorf("不支持的网盘类型: %s", t.provider)
	}
}

type baiduDrive struct {
	m *auth.Manager
}

func (d *baiduDrive) Provider() string { return config.ProviderBaidu }

func (d *baiduDrive) CleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("云端路径不能为空")
	}
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("百度网盘云端路径必须是以 /apps/ 开头的绝对路径: %s", p)
	}
	return p, nil
}

func (d *baiduDrive) Quota() (*api.Quota, error) {
	var q *api.Quota
	err := d.m.Do(func() error {
		var e error
		q, e = d.m.Client.GetQuota()
		return e
	})
	return q, err
}

func (d *baiduDrive) ListDir(dir string) ([]api.Entry, error) {
	var entries []api.Entry
	err := d.m.Do(func() error {
		var e error
		entries, e = d.m.Client.ListDir(dir)
		return e
	})
	return entries, err
}

func (d *baiduDrive) ListAll(dir string) ([]api.Entry, error) {
	var entries []api.Entry
	err := d.m.Do(func() error {
		var e error
		entries, e = d.m.Client.ListAll(dir)
		return e
	})
	return entries, err
}

func (d *baiduDrive) UploadFile(localPath, remotePath string) (*api.UploadResult, error) {
	return d.Uploader().UploadFile(localPath, remotePath)
}

func (d *baiduDrive) Uploader() sync.Uploader {
	return sync.ManagedUploader{Client: d.m.Client, Do: d.m.Do}
}

func (d *baiduDrive) Lister() sync.Lister { return d }

type weiyunDrive struct {
	c *weiyun.Client
}

func (d *weiyunDrive) Provider() string { return config.ProviderWeiyun }

func (d *weiyunDrive) CleanPath(p string) (string, error) { return weiyun.CleanPath(p) }

func (d *weiyunDrive) Quota() (*api.Quota, error) {
	return nil, fmt.Errorf("微云 MCP 接口未提供容量查询；可用 ls 验证令牌与目录")
}

func (d *weiyunDrive) ListDir(dir string) ([]api.Entry, error) { return d.c.ListDir(dir) }

func (d *weiyunDrive) UploadFile(localPath, remotePath string) (*api.UploadResult, error) {
	return d.c.UploadFile(localPath, remotePath)
}

func (d *weiyunDrive) Uploader() sync.Uploader { return d.c }
func (d *weiyunDrive) Lister() sync.Lister     { return d.c }

func endpointsFor(override string) api.Endpoints {
	if override == "" {
		return api.Endpoints{}
	}
	return api.Endpoints{Auth: override, Pan: override, PCS: override}
}
