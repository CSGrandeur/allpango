package api

import (
	"encoding/json"
	"net/http"
	"net/url"
)

// TokenResponse 是 OAuth 令牌接口的成功响应。
// refresh_token 一次性：每次刷新都会换新的，旧的立即作废，必须写回文件。
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// RefreshToken 用 refresh token 换新的 access token。
// 成功后调用者必须把返回的新 refresh token 持久化，旧的已作废。
func (c *Client) RefreshToken(refreshToken string) (*TokenResponse, error) {
	ep := c.Endpoints.withDefaults()
	var out TokenResponse
	err := c.call("刷新令牌", func() (*http.Request, error) {
		q := url.Values{}
		q.Set("grant_type", "refresh_token")
		q.Set("refresh_token", refreshToken)
		q.Set("client_id", c.AppKey)
		q.Set("client_secret", c.AppSecret)
		return http.NewRequest(http.MethodGet, queryURL(ep.Auth, "/oauth/2.0/token", q), nil)
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DeviceCode 是设备码授权第一步的响应。
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	QRCodeURL       string `json:"qrcode_url"`
	ExpiresIn       int64  `json:"expires_in"`
	Interval        int64  `json:"interval"`
}

// RequestDeviceCode 申请设备码与用户码。用户在浏览器完成确认前，
// 用 DeviceToken 轮询换取令牌，间隔不低于返回的 interval（官方最低 5 秒）。
func (c *Client) RequestDeviceCode() (*DeviceCode, error) {
	ep := c.Endpoints.withDefaults()
	var out DeviceCode
	err := c.call("获取设备码", func() (*http.Request, error) {
		q := url.Values{}
		q.Set("response_type", "device_code")
		q.Set("client_id", c.AppKey)
		q.Set("scope", "basic,netdisk")
		return http.NewRequest(http.MethodGet, queryURL(ep.Auth, "/oauth/2.0/device/code", q), nil)
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.Interval < 5 {
		out.Interval = 5
	}
	return &out, nil
}

// DevicePendingError 表示用户还没有完成授权，继续轮询。
type DevicePendingError struct{ Reason string }

func (e *DevicePendingError) Error() string { return "等待用户授权：" + e.Reason }

// DeviceToken 用设备码换令牌。用户未确认时返回 *DevicePendingError；
// slow_down 也算等待。设备码过期等其余错误原样返回。
func (c *Client) DeviceToken(deviceCode string) (*TokenResponse, error) {
	ep := c.Endpoints.withDefaults()
	q := url.Values{}
	q.Set("grant_type", "device_token")
	q.Set("code", deviceCode)
	q.Set("client_id", c.AppKey)
	q.Set("client_secret", c.AppSecret)
	r, err := http.NewRequest(http.MethodGet, queryURL(ep.Auth, "/oauth/2.0/token", q), nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("User-Agent", UserAgent)
	resp, err := c.httpClient().Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	var probe struct {
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(raw, &probe)
	switch probe.Error {
	case "authorization_pending", "slow_down":
		return nil, &DevicePendingError{Reason: probe.Error}
	case "expired_token", "access_denied":
		return nil, &APIError{Op: "设备码换令牌", Errno: -1, ErrMsg: probe.Error + ": " + probe.ErrorDesc}
	case "":
		var out TokenResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		if out.AccessToken == "" {
			return nil, &APIError{Op: "设备码换令牌", Errno: -1, ErrMsg: "响应缺少 access_token"}
		}
		return &out, nil
	default:
		return nil, &APIError{Op: "设备码换令牌", Errno: -1, ErrMsg: probe.Error + ": " + probe.ErrorDesc}
	}
}
