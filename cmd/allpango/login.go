package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/CSGrandeur/allpango/internal/api"
	"github.com/CSGrandeur/allpango/internal/auth"
	"github.com/CSGrandeur/allpango/internal/config"
	"github.com/CSGrandeur/allpango/internal/weiyun"
)

// runLogin 按目标网盘执行授权并写入 --token 指定的文件。不替用户完成浏览器确认。
func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	c := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	t, err := c.resolveSingleTarget(true)
	if err != nil {
		return err
	}
	switch t.provider {
	case config.ProviderBaidu:
		return loginBaidu(t)
	case config.ProviderWeiyun:
		return loginWeiyun(t)
	default:
		return fmt.Errorf("不支持的网盘类型: %s", t.provider)
	}
}

func loginBaidu(t target) error {
	cred, err := auth.LoadCredentials(t.credPath)
	if err != nil {
		return err
	}
	ep := endpointsFor(t.endpoint)
	client := &api.Client{
		AppKey: cred.AppKey, AppSecret: cred.AppSecret,
		Endpoints: ep, MaxRetries: 2, Logf: logfFor(t.verbose),
	}
	if t.hasRetryBase {
		client.RetryBase = t.retryBase
	}

	dc, err := client.RequestDeviceCode()
	if err != nil {
		return err
	}
	fmt.Println("请在浏览器打开下面的网址，登录百度账号并输入用户码完成授权：")
	fmt.Println()
	fmt.Printf("  验证网址: %s\n", dc.VerificationURL)
	fmt.Printf("  用户码:   %s\n", dc.UserCode)
	if dc.QRCodeURL != "" {
		fmt.Printf("  或用手机扫码: %s\n", dc.QRCodeURL)
	}
	fmt.Println()
	fmt.Printf("设备码 %d 秒内有效，等待授权中……\n", dc.ExpiresIn)

	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	interval := time.Duration(dc.Interval) * time.Second
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("设备码已过期，请重新运行 login")
		}
		time.Sleep(interval)
		resp, err := client.DeviceToken(dc.DeviceCode)
		if err != nil {
			if _, pending := err.(*api.DevicePendingError); pending {
				continue
			}
			return err
		}
		m, err := auth.NewManager(cred, t.tokenPath, ep, logfFor(t.verbose))
		if err != nil {
			return err
		}
		if err := m.SaveNewToken(resp); err != nil {
			return err
		}
		fmt.Printf("授权成功，令牌已写入 %s\n", t.tokenPath)
		fmt.Println("请妥善保管该文件，不要提交到代码仓库。")
		return nil
	}
}

func loginWeiyun(t target) error {
	existing, err := weiyun.LoadTokenFile(t.tokenPath)
	if err != nil {
		return err
	}

	fmt.Println("微云使用 OpenClaw MCP 令牌，请打开下面的页面登录并复制 mcp_token：")
	fmt.Println()
	fmt.Printf("  获取地址: %s\n", weiyun.OpenClawURL)
	fmt.Println()
	fmt.Print("请粘贴 mcp_token（支持裸令牌或 JSON，输入后回车）：")

	raw, eof, err := readTokenFromStdin()
	if err != nil {
		return fmt.Errorf("读取输入失败: %w", err)
	}

	var tok string
	if eof && existing.HasToken() {
		tok = existing.MCPToken
	} else {
		tok, err = weiyun.ParseTokenInput(raw)
		if err != nil {
			return err
		}
	}

	cl := &weiyun.Client{
		MCPToken:   tok,
		EnvID:      existing.EnvID,
		MCPURL:     t.endpoint,
		MaxRetries: 3,
		Logf:       logfFor(t.verbose),
	}
	if t.hasRetryBase {
		cl.RetryBase = t.retryBase
	}
	if err := cl.VerifyToken(); err != nil {
		return fmt.Errorf("微云令牌校验未通过: %w", err)
	}

	tf := &weiyun.TokenFile{MCPToken: tok, EnvID: existing.EnvID}
	if err := tf.Save(t.tokenPath); err != nil {
		return err
	}
	fmt.Printf("授权验证成功，微云令牌已写入 %s（权限 0600）\n", t.tokenPath)
	fmt.Println("请妥善保管该文件，不要提交到代码仓库。")
	return nil
}

// readTokenFromStdin 读入单行令牌或多行 JSON，并在末尾立即 EOF 时返回 eof=true。
func readTokenFromStdin() (raw string, eof bool, err error) {
	sc := bufio.NewScanner(os.Stdin)
	var buf strings.Builder
	isJSON := false
	for sc.Scan() {
		line := sc.Text()
		if buf.Len() == 0 && strings.TrimSpace(line) == "" {
			continue
		}
		if buf.Len() == 0 && strings.HasPrefix(strings.TrimSpace(line), "{") {
			isJSON = true
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
		if !isJSON || json.Valid([]byte(buf.String())) {
			return buf.String(), false, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", false, err
	}
	if buf.Len() == 0 {
		return "", true, nil
	}
	return buf.String(), false, nil
}

// runRefresh 刷新令牌（百度）或验证固定令牌可用（微云）。
func runRefresh(args []string) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	c := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	t, err := c.resolveSingleTarget(false)
	if err != nil {
		return err
	}
	if t.provider == config.ProviderWeiyun {
		tf, err := weiyun.LoadTokenFile(t.tokenPath)
		if err != nil {
			return err
		}
		if !tf.HasToken() {
			return fmt.Errorf("还没有微云令牌，请先运行 allpango login -p weiyun --token %s", t.tokenPath)
		}
		cl := &weiyun.Client{
			MCPToken:   tf.MCPToken,
			EnvID:      tf.EnvID,
			MCPURL:     t.endpoint,
			MaxRetries: 3,
			Logf:       logfFor(t.verbose),
		}
		if t.hasRetryBase {
			cl.RetryBase = t.retryBase
		}
		if err := cl.VerifyToken(); err != nil {
			return err
		}
		fmt.Println("微云使用固定 mcp_token，无需刷新；已验证令牌有效")
		return nil
	}

	cred, err := auth.LoadCredentials(t.credPath)
	if err != nil {
		return err
	}
	m, err := auth.NewManager(cred, t.tokenPath, endpointsFor(t.endpoint), logfFor(t.verbose))
	if err != nil {
		return err
	}
	if t.hasRetryBase {
		m.Client.RetryBase = t.retryBase
	}
	if !m.HasToken() {
		return fmt.Errorf("还没有令牌，请先运行 allpango login")
	}
	if err := m.ForceRefresh(); err != nil {
		return err
	}
	fmt.Printf("令牌已刷新并写回 %s\n", t.tokenPath)
	return nil
}
