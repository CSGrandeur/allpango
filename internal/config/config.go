// Package config 读取并校验 allpango 多网盘配置文件与直连模式网盘识别。
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ProviderBaidu  = "baidu"
	ProviderWeiyun = "weiyun"
)

var inlineSecretKeys = map[string]bool{
	"app_key":       true,
	"app_secret":    true,
	"access_token":  true,
	"refresh_token": true,
	"mcp_token":     true,
	"secret":        true,
	"password":      true,
	"passwd":        true,
}

// File 表示 allpango 多网盘配置文件。
type File struct {
	Remotes map[string]RemoteConfig `json:"remotes"`
}

// RemoteConfig 描述一个已配置的网盘目标。
type RemoteConfig struct {
	Provider string `json:"provider"`
	Cred     string `json:"cred,omitempty"`
	Token    string `json:"token"`
	Path     string `json:"path,omitempty"`
	Manifest string `json:"manifest,omitempty"`
	Parallel int    `json:"parallel,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

// NamedRemote 是带名称与已展开路径的网盘配置。
type NamedRemote struct {
	Name   string
	Config RemoteConfig
}

// Load 读取并严格校验配置文件，将其中相对文件路径按配置文件所在目录展开。
func Load(path string) (*File, error) {
	absCfg, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析配置路径失败: %w", err)
	}
	baseDir := filepath.Dir(absCfg)
	data, err := os.ReadFile(absCfg)
	if err != nil {
		return nil, fmt.Errorf("读配置文件失败: %w", err)
	}
	if key := findInlineSecretKey(data); key != "" {
		return nil, fmt.Errorf("配置文件不能直接存放密钥或令牌（发现字段 %q）；请写入独立的 0600 文件并在配置里填写文件路径", key)
	}

	var cfg File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("配置文件格式不合法: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("配置文件包含多余内容")
	}
	if len(cfg.Remotes) == 0 {
		return nil, fmt.Errorf("配置文件缺少非空的 remotes")
	}

	manifestOwners := make(map[string]string, len(cfg.Remotes))
	names := make([]string, 0, len(cfg.Remotes))
	for name := range cfg.Remotes {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := validateRemoteName(name); err != nil {
			return nil, err
		}
		rc := cfg.Remotes[name]
		rc.Provider = strings.ToLower(strings.TrimSpace(rc.Provider))
		rc.Cred = strings.TrimSpace(rc.Cred)
		rc.Token = strings.TrimSpace(rc.Token)
		rc.Path = strings.TrimSpace(rc.Path)
		rc.Manifest = strings.TrimSpace(rc.Manifest)
		rc.Endpoint = strings.TrimSpace(rc.Endpoint)

		switch rc.Provider {
		case ProviderBaidu:
			if rc.Cred == "" {
				return nil, fmt.Errorf("remote %q（baidu）缺少 cred 文件路径", name)
			}
			if rc.Token == "" {
				return nil, fmt.Errorf("remote %q（baidu）缺少 token 文件路径", name)
			}
		case ProviderWeiyun:
			if rc.Cred != "" {
				return nil, fmt.Errorf("remote %q（weiyun）不需要 cred 字段，仅配置 token 即可", name)
			}
			if rc.Token == "" {
				return nil, fmt.Errorf("remote %q（weiyun）缺少 token 文件路径", name)
			}
		default:
			return nil, fmt.Errorf("remote %q 不支持的 provider %q（可选: baidu, weiyun）", name, rc.Provider)
		}
		if rc.Parallel < 0 {
			return nil, fmt.Errorf("remote %q 的 parallel 不能为负数", name)
		}

		if rc.Cred != "" {
			p, err := expandPath(baseDir, rc.Cred)
			if err != nil {
				return nil, fmt.Errorf("remote %q cred 路径无效: %w", name, err)
			}
			rc.Cred = p
		}
		p, err := expandPath(baseDir, rc.Token)
		if err != nil {
			return nil, fmt.Errorf("remote %q token 路径无效: %w", name, err)
		}
		rc.Token = p

		if rc.Manifest != "" {
			mp, err := expandPath(baseDir, rc.Manifest)
			if err != nil {
				return nil, fmt.Errorf("remote %q manifest 路径无效: %w", name, err)
			}
			rc.Manifest = mp
			absM, err := filepath.Abs(mp)
			if err != nil {
				absM = mp
			}
			if prev, exists := manifestOwners[absM]; exists {
				return nil, fmt.Errorf("remote %q 与 %q 共用了同一个 manifest 文件 %s，各网盘须使用独立清单", prev, name, mp)
			}
			manifestOwners[absM] = name
		}
		cfg.Remotes[name] = rc
	}
	return &cfg, nil
}

// Names 返回按字典序排列的全部 remote 名称。
func (f *File) Names() []string {
	names := make([]string, 0, len(f.Remotes))
	for name := range f.Remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Select 根据 -r / --all 选择目标网盘；当配置仅含一个网盘且未指定时默认选中它。
func (f *File) Select(spec string, all bool) ([]NamedRemote, error) {
	if all && strings.TrimSpace(spec) != "" {
		return nil, fmt.Errorf("--all 与 -r 不能同时使用")
	}
	names := f.Names()
	if all {
		out := make([]NamedRemote, 0, len(names))
		for _, n := range names {
			out = append(out, NamedRemote{Name: n, Config: f.Remotes[n]})
		}
		return out, nil
	}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		if len(names) == 1 {
			n := names[0]
			return []NamedRemote{{Name: n, Config: f.Remotes[n]}}, nil
		}
		return nil, fmt.Errorf("配置中有多个网盘（%s），请用 -r 指定名称或传 --all", strings.Join(names, ", "))
	}

	parts := strings.Split(spec, ",")
	seen := make(map[string]bool, len(parts))
	out := make([]NamedRemote, 0, len(parts))
	for _, raw := range parts {
		n := strings.TrimSpace(raw)
		if n == "" {
			return nil, fmt.Errorf("-r 参数格式无效: %q", spec)
		}
		rc, ok := f.Remotes[n]
		if !ok {
			return nil, fmt.Errorf("配置中不存在 remote %q（可选: %s）", n, strings.Join(names, ", "))
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, NamedRemote{Name: n, Config: rc})
	}
	return out, nil
}

// DetectProviderFromTokenFile 根据已有令牌文件的顶层字段自动识别网盘类型。
// 文件不存在或无法判断时返回空串。
func DetectProviderFromTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("读令牌文件失败: %w", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", fmt.Errorf("令牌文件不是合法 JSON: %w", err)
	}
	if s, ok := obj["mcp_token"].(string); ok && strings.TrimSpace(s) != "" {
		return ProviderWeiyun, nil
	}
	if s, ok := obj["access_token"].(string); ok && strings.TrimSpace(s) != "" {
		return ProviderBaidu, nil
	}
	if s, ok := obj["refresh_token"].(string); ok && strings.TrimSpace(s) != "" {
		return ProviderBaidu, nil
	}
	return "", nil
}

func validateRemoteName(name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return fmt.Errorf("remote 名称不能为空或含首尾空白: %q", name)
	}
	if name == "all" {
		return fmt.Errorf("remote 名称不能使用保留字 \"all\"")
	}
	if strings.ContainsAny(name, ", \t\r\n") {
		return fmt.Errorf("remote 名称不能包含逗号或空白字符: %q", name)
	}
	return nil
}

func expandPath(baseDir, raw string) (string, error) {
	if raw == "~" || strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if raw == "~" {
			return home, nil
		}
		return filepath.Clean(filepath.Join(home, raw[2:])), nil
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	return filepath.Clean(filepath.Join(baseDir, raw)), nil
}

func findInlineSecretKey(data []byte) string {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return ""
	}
	var walk func(v any) string
	walk = func(v any) string {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if inlineSecretKeys[strings.ToLower(k)] {
					return k
				}
				if hit := walk(val); hit != "" {
					return hit
				}
			}
		case []any:
			for _, item := range x {
				if hit := walk(item); hit != "" {
					return hit
				}
			}
		}
		return ""
	}
	return walk(root)
}
