package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/CSGrandeur/allpango/internal/api"
	"github.com/CSGrandeur/allpango/internal/auth"
)

// commonFlags 是所有命令共用的参数。凭证与令牌路径必须由调用者给出，不猜默认。
type commonFlags struct {
	cred      string
	token     string
	endpoint  string // 可选：接口地址覆盖（测试用），默认官方地址
	retryBase string // 可选：重试退避基数（测试用），默认 1s
	verbose   bool
}

func addCommonFlags(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.cred, "cred", "", "凭证文件路径（JSON：app_key / app_secret）")
	fs.StringVar(&c.token, "token", "", "令牌文件路径（login 创建，程序自动写回）")
	fs.StringVar(&c.endpoint, "endpoint", "", "接口地址覆盖，仅测试用；默认官方地址")
	fs.StringVar(&c.retryBase, "retry-base", "", "重试退避基数（如 10ms），仅测试用；默认 1s")
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

// setup 加载凭证与令牌，构造管理器。requireToken 为 true 时要求已有令牌。
func (c *commonFlags) setup(requireToken bool) (*auth.Manager, error) {
	if c.cred == "" {
		return nil, fmt.Errorf("缺少 --cred 凭证文件")
	}
	if c.token == "" {
		return nil, fmt.Errorf("缺少 --token 令牌文件")
	}
	cred, err := auth.LoadCredentials(c.cred)
	if err != nil {
		return nil, err
	}
	ep := api.Endpoints{}
	if c.endpoint != "" {
		ep = api.Endpoints{Auth: c.endpoint, Pan: c.endpoint, PCS: c.endpoint}
	}
	m, err := auth.NewManager(cred, c.token, ep, logfFor(c.verbose))
	if err != nil {
		return nil, err
	}
	if c.retryBase != "" {
		d, err := time.ParseDuration(c.retryBase)
		if err != nil {
			return nil, fmt.Errorf("--retry-base 不合法: %w", err)
		}
		m.Client.RetryBase = d
	}
	if requireToken && !m.HasToken() {
		return nil, fmt.Errorf("还没有令牌，请先运行 allpango login")
	}
	return m, nil
}
