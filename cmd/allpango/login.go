package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/CSGrandeur/allpango/internal/api"
	"github.com/CSGrandeur/allpango/internal/auth"
)

// runLogin 走设备码授权：打印用户码和验证网址，等用户在浏览器确认，
// 拿到令牌后只写入 --token 指定的文件。不替用户完成浏览器确认。
func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	c := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if c.cred == "" || c.token == "" {
		return fmt.Errorf("login 需要 --cred 和 --token")
	}
	cred, err := auth.LoadCredentials(c.cred)
	if err != nil {
		return err
	}
	ep := endpointsFor(c.endpoint)
	client := &api.Client{
		AppKey: cred.AppKey, AppSecret: cred.AppSecret,
		Endpoints: ep, MaxRetries: 2, Logf: logfFor(c.verbose),
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
		m, err := auth.NewManager(cred, c.token, ep, logfFor(c.verbose))
		if err != nil {
			return err
		}
		if err := m.SaveNewToken(resp); err != nil {
			return err
		}
		fmt.Printf("授权成功，令牌已写入 %s\n", c.token)
		fmt.Println("请妥善保管该文件，不要提交到代码仓库。")
		return nil
	}
}

// runRefresh 强制刷新一次令牌并把新 refresh token 写回。
func runRefresh(args []string) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	c := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := c.setup(true)
	if err != nil {
		return err
	}
	if err := m.ForceRefresh(); err != nil {
		return err
	}
	fmt.Printf("令牌已刷新并写回 %s\n", c.token)
	return nil
}

func endpointsFor(override string) api.Endpoints {
	if override == "" {
		return api.Endpoints{}
	}
	return api.Endpoints{Auth: override, Pan: override, PCS: override}
}
