package auth

import (
	"fmt"
	"sync"
	"time"

	"github.com/CSGrandeur/allpango/internal/api"
)

// Manager 把令牌文件、凭证和 API 客户端接起来：
// 按需刷新、失效时刷新一次重试、新 refresh token 立即写回。
type Manager struct {
	Client    *api.Client
	TokenPath string

	mu sync.Mutex
	tf *TokenFile
}

// NewManager 读取令牌文件并构造管理器。凭证必须已加载。
func NewManager(cred *Credentials, tokenPath string, ep api.Endpoints, logf func(string, ...any)) (*Manager, error) {
	tf, err := LoadTokenFile(tokenPath)
	if err != nil {
		return nil, err
	}
	m := &Manager{TokenPath: tokenPath, tf: tf}
	m.Client = &api.Client{
		AppKey:     cred.AppKey,
		AppSecret:  cred.AppSecret,
		Token:      m.accessToken,
		Endpoints:  ep,
		MaxRetries: 3,
		Logf:       logf,
	}
	return m, nil
}

func (m *Manager) accessToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tf.AccessToken
}

// HasToken 报告是否已有 access token（不保证有效）。
func (m *Manager) HasToken() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tf.AccessToken != ""
}

// EnsureFresh 在令牌缺失或临近到期时刷新。刷新得到的新 refresh token 立即写回。
func (m *Manager) EnsureFresh() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.tf.NeedsRefresh(time.Now()) {
		return nil
	}
	return m.refreshLocked()
}

// ForceRefresh 无条件刷新一次（login 后验证、或 access token 被判失效时）。
func (m *Manager) ForceRefresh() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshLocked()
}

func (m *Manager) refreshLocked() error {
	if m.tf.RefreshToken == "" {
		return fmt.Errorf("没有 refresh token，请先运行 allpango login")
	}
	resp, err := m.Client.RefreshToken(m.tf.RefreshToken)
	if err != nil {
		return fmt.Errorf("刷新令牌失败（refresh token 可能已失效，需要重新 login）: %w", err)
	}
	m.tf.ApplyRefresh(resp.AccessToken, resp.RefreshToken, resp.ExpiresIn, resp.Scope, time.Now())
	if err := m.tf.Save(m.TokenPath); err != nil {
		// 新 refresh token 已在内存里但没落盘：进程退出就丢了，必须当错误报。
		return fmt.Errorf("刷新成功但写回令牌文件失败，请立即重试以免令牌丢失: %w", err)
	}
	return nil
}

// SaveNewToken 在 login 拿到全新令牌后写回。
func (m *Manager) SaveNewToken(resp *api.TokenResponse) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tf.ApplyRefresh(resp.AccessToken, resp.RefreshToken, resp.ExpiresIn, resp.Scope, time.Now())
	return m.tf.Save(m.TokenPath)
}

// Do 执行一次 API 调用：先确保令牌新鲜；若返回令牌失效错误，强制刷新后重试一次。
// 仍然失效则报错，提示重新登录。
func (m *Manager) Do(op func() error) error {
	if err := m.EnsureFresh(); err != nil {
		return err
	}
	err := op()
	if err == nil {
		return nil
	}
	if ae, ok := err.(*api.APIError); ok && ae.AuthInvalid() {
		if rerr := m.ForceRefresh(); rerr != nil {
			return rerr
		}
		err = op()
		if err == nil {
			return nil
		}
		if ae2, ok := err.(*api.APIError); ok && ae2.AuthInvalid() {
			return fmt.Errorf("令牌刷新后仍无效，请重新运行 allpango login: %w", err)
		}
	}
	return err
}
