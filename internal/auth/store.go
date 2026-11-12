// Package auth 管理凭证与令牌文件。密钥和令牌只从调用者指定的文件读写，
// 不进源码、不进日志。refresh token 一次性：刷新成功后立即原子写回。
package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Credentials 是凭证文件内容：开放平台应用的 AppKey / SecretKey。
type Credentials struct {
	AppKey    string `json:"app_key"`
	AppSecret string `json:"app_secret"`
}

// LoadCredentials 读取并校验凭证文件。
func LoadCredentials(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读凭证文件失败: %w", err)
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("凭证文件不是合法 JSON: %w", err)
	}
	if c.AppKey == "" || c.AppSecret == "" {
		return nil, fmt.Errorf("凭证文件缺少 app_key 或 app_secret")
	}
	return &c, nil
}

// TokenFile 是令牌文件内容。access_token_expires_at 是本地推算的到期时刻
// （Unix 秒），仅用于判断是否需要刷新；外部工具写的文件可以没有它。
type TokenFile struct {
	AccessToken       string `json:"access_token"`
	RefreshToken      string `json:"refresh_token"`
	ExpiresIn         int64  `json:"expires_in,omitempty"`
	AccessTokenExpiry int64  `json:"access_token_expires_at,omitempty"`
	Scope             string `json:"scope,omitempty"`
}

// LoadTokenFile 读取令牌文件。文件不存在时返回零值（尚未登录）。
func LoadTokenFile(path string) (*TokenFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &TokenFile{}, nil
		}
		return nil, fmt.Errorf("读令牌文件失败: %w", err)
	}
	var t TokenFile
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("令牌文件不是合法 JSON: %w", err)
	}
	return &t, nil
}

// Save 原子写回令牌文件：先写临时文件再改名，权限 0600。
// 刷新返回新 refresh token 后必须立刻调用，旧 token 已作废。
func (t *TokenFile) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建令牌目录失败: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".allpango-token-*")
	if err != nil {
		return fmt.Errorf("写令牌文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写令牌文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换令牌文件失败: %w", err)
	}
	return nil
}

// NeedsRefresh 判断 access token 是否缺失或临近到期（提前 1 小时）。
// 有 access token 但没有到期信息时（外部工具写入的文件）先直接用，
// 失效时由调用方触发刷新，避免每次启动都作废一个 refresh token。
func (t *TokenFile) NeedsRefresh(now time.Time) bool {
	if t.AccessToken == "" || t.RefreshToken == "" {
		return true
	}
	if t.AccessTokenExpiry == 0 {
		return false
	}
	return now.Unix()+3600 >= t.AccessTokenExpiry
}

// ApplyRefresh 把刷新响应写进令牌结构。返回的新 refresh token 为空时保留旧值
// （个别响应可能不带），正常情况下每次都会换新。
func (t *TokenFile) ApplyRefresh(accessToken, refreshToken string, expiresIn int64, scope string, now time.Time) {
	t.AccessToken = accessToken
	if refreshToken != "" {
		t.RefreshToken = refreshToken
	}
	t.ExpiresIn = expiresIn
	t.AccessTokenExpiry = now.Unix() + expiresIn
	if scope != "" {
		t.Scope = scope
	}
}
