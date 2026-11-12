// Package api 实现百度网盘开放平台客户端。只做备份需要的能力：
// 刷新令牌、设备码登录、容量、列目录、上传（预上传、分片、合并）、建目录。
// 没有删除和移动。
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 官方接口地址。分片上传的域名不在此列：向「获取上传域名」接口要，不写死。
const (
	DefaultAuthBase = "https://openapi.baidu.com"
	DefaultPanBase  = "https://pan.baidu.com"
	DefaultPCSBase  = "https://d.pcs.baidu.com"

	// 官方文档要求上传等接口携带此 User-Agent。
	UserAgent = "pan.baidu.com"
)

// Endpoints 汇总三组接口地址，默认官方地址，测试时指向模拟接口。
type Endpoints struct {
	Auth string // OAuth：设备码、换令牌、刷新令牌
	Pan  string // xpan 文件接口与容量
	PCS  string // 「获取上传域名」入口
}

func (e Endpoints) withDefaults() Endpoints {
	if e.Auth == "" {
		e.Auth = DefaultAuthBase
	}
	if e.Pan == "" {
		e.Pan = DefaultPanBase
	}
	if e.PCS == "" {
		e.PCS = DefaultPCSBase
	}
	return e
}

// Client 是一个账号的客户端。AppKey/AppSecret 来自调用者给的凭证文件；
// Token 返回当前 access token，刷新后这里要能拿到新值。令牌和密钥不落日志。
type Client struct {
	AppKey    string
	AppSecret string
	Token     func() string

	Endpoints Endpoints
	HTTP      *http.Client

	// MaxRetries 是单个请求的额外重试次数（网络错误、429、5xx、频控业务码）。
	MaxRetries int
	// RetryBase 是重试退避基数，默认 1s，指数增长，上限 30s。
	RetryBase time.Duration
	// ChunkSize 是上传分片大小，默认 4MB（普通用户的规定值）。
	// 分片数上限 1024，超过时自动放大分片。
	ChunkSize int64
	// Logf 输出进度日志。不得把令牌、密钥传进来。
	Logf func(format string, args ...any)
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func (c *Client) accessToken() string {
	if c.Token == nil {
		return ""
	}
	return c.Token()
}

func (c *Client) retries() int {
	if c.MaxRetries < 0 {
		return 0
	}
	return c.MaxRetries
}

func (c *Client) backoff(attempt int) time.Duration {
	base := c.RetryBase
	if base <= 0 {
		base = time.Second
	}
	wait := base * (1 << (attempt - 1))
	if wait > 30*time.Second {
		wait = 30 * time.Second
	}
	return wait
}

// APIError 是业务错误码非 0。errno 来自 xpan/oauth 接口，error_code 来自 PCS 接口。
type APIError struct {
	Op      string
	Errno   int
	ErrMsg  string
	ShowMsg string
}

func (e *APIError) Error() string {
	msg := e.ErrMsg
	if msg == "" {
		msg = e.ShowMsg
	}
	if msg == "" {
		msg = errnoText(e.Errno)
	}
	return fmt.Sprintf("%s: 百度接口返回错误 %d（%s）", e.Op, e.Errno, msg)
}

// AuthInvalid 表示令牌失效或被撤销，需要刷新或重新授权。
// -6 也算：身份验证失败时先尝试刷新一次。
func (e *APIError) AuthInvalid() bool {
	switch e.Errno {
	case 110, 111, -6:
		return true
	}
	return false
}

// retryableErrno 表示这个业务错误可以稍后重试（频控等）。
func retryableErrno(n int) bool {
	switch n {
	case 4, 17, 18, 19, 31034:
		return true
	}
	return false
}

func errnoText(n int) string {
	switch n {
	case -1:
		return "云端内部错误"
	case -6:
		return "身份验证失败，令牌可能失效"
	case -7:
		return "文件或目录名错误或无权访问"
	case -8:
		return "文件或目录已存在"
	case -9:
		return "文件或目录不存在"
	case -10:
		return "云端容量不足"
	case 2:
		return "参数错误"
	case 4:
		return "请求过于频繁"
	case 110:
		return "access token 无效"
	case 111:
		return "access token 过期"
	case 31034:
		return "命中接口频控"
	case 31066, 31190:
		return "文件不存在"
	default:
		return "见开放平台错误码文档"
	}
}

// HTTPStatusError 包装 HTTP 层失败。
type HTTPStatusError struct {
	Op     string
	Status int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d", e.Op, e.Status)
}

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// doRaw 发请求并读回响应体。网络错误、429、5xx 自动重试。
// newReq 每次重试都要能构造全新请求（请求体可重放）。
func (c *Client) doRaw(op string, newReq func() (*http.Request, error)) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries(); attempt++ {
		if attempt > 0 {
			wait := c.backoff(attempt)
			c.logf("%s: 第 %d 次重试（%v 后）：%v", op, attempt, wait, lastErr)
			time.Sleep(wait)
		}
		r, err := newReq()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		r.Header.Set("User-Agent", UserAgent)
		resp, err := c.httpClient().Do(r)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", op, err)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = &HTTPStatusError{Op: op, Status: resp.StatusCode}
			if retryableStatus(resp.StatusCode) {
				continue
			}
			return nil, lastErr
		}
		if readErr != nil {
			lastErr = fmt.Errorf("%s: 读响应失败: %w", op, readErr)
			continue
		}
		return body, nil
	}
	return nil, lastErr
}

// checkErrno 解析业务错误码。PCS 用 error_code，xpan/oauth 用 errno。
func checkErrno(op string, body []byte) error {
	var probe struct {
		Errno     *int   `json:"errno"`
		ErrorCode *int   `json:"error_code"`
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
		ErrorMsg  string `json:"error_msg"`
		ShowMsg   string `json:"show_msg"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return fmt.Errorf("%s: 响应不是合法 JSON: %w", op, err)
	}
	n := 0
	if probe.Errno != nil {
		n = *probe.Errno
	} else if probe.ErrorCode != nil {
		n = *probe.ErrorCode
	}
	if n == 0 && probe.Error == "" {
		return nil
	}
	msg := probe.ErrorMsg
	if msg == "" {
		msg = probe.ErrorDesc
	}
	if msg == "" {
		msg = probe.Error
	}
	return &APIError{Op: op, Errno: n, ErrMsg: msg, ShowMsg: probe.ShowMsg}
}

// call 调接口并把结果解析进 out；频控业务码与传输错误一样重试。out 可为 nil。
func (c *Client) call(op string, newReq func() (*http.Request, error), out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.retries(); attempt++ {
		if attempt > 0 {
			wait := c.backoff(attempt)
			c.logf("%s: 第 %d 次重试（%v 后）：%v", op, attempt, wait, lastErr)
			time.Sleep(wait)
		}
		body, err := c.doRaw(op, newReq)
		if err != nil {
			return err // doRaw 已按传输错误重试过
		}
		if err := checkErrno(op, body); err != nil {
			lastErr = err
			if ae, ok := err.(*APIError); ok && retryableErrno(ae.Errno) {
				continue
			}
			return err
		}
		if out != nil {
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("%s: 解析响应失败: %w", op, err)
			}
		}
		return nil
	}
	return lastErr
}

func queryURL(base, path string, q url.Values) string {
	return strings.TrimRight(base, "/") + path + "?" + q.Encode()
}
