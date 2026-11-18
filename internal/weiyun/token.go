package weiyun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// OpenClawURL 是微云官方 MCP 令牌的获取页面。
const OpenClawURL = "https://www.weiyun.com/act/openclaw"

// TokenFile 保存微云 MCP 令牌；格式与官方 OpenClaw 令牌文件兼容。
type TokenFile struct {
	MCPToken string `json:"mcp_token"`
	EnvID    string `json:"env_id,omitempty"`
}

// HasToken 报告是否已填写非空 mcp_token。
func (t *TokenFile) HasToken() bool {
	return t != nil && strings.TrimSpace(t.MCPToken) != ""
}

// LoadTokenFile 读取微云令牌文件；文件不存在时返回零值。
func LoadTokenFile(path string) (*TokenFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &TokenFile{}, nil
		}
		return nil, fmt.Errorf("读微云令牌文件失败: %w", err)
	}
	var tf TokenFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("微云令牌文件不是合法 JSON: %w", err)
	}
	tf.MCPToken = strings.TrimSpace(tf.MCPToken)
	tf.EnvID = strings.TrimSpace(tf.EnvID)
	return &tf, nil
}

// Save 原子写回微云令牌文件，权限 0600。
func (t *TokenFile) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建令牌目录失败: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".allpango-weiyun-*")
	if err != nil {
		return fmt.Errorf("写微云令牌文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写微云令牌文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换微云令牌文件失败: %w", err)
	}
	return nil
}

// ParseTokenInput 解析用户粘贴的 mcp_token：支持 JSON 对象、mcp_token=... 片段或裸令牌。
// 报错绝不回显原始输入，避免密钥进入终端输出或日志。
func ParseTokenInput(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("输入为空，请粘贴从 %s 获取的 mcp_token", OpenClawURL)
	}
	if strings.HasPrefix(s, "{") {
		var obj struct {
			MCPToken string `json:"mcp_token"`
		}
		if err := json.Unmarshal([]byte(s), &obj); err != nil {
			return "", fmt.Errorf("粘贴的 JSON 格式不合法，应含 \"mcp_token\" 字段")
		}
		s = strings.TrimSpace(obj.MCPToken)
		if s == "" {
			return "", fmt.Errorf("粘贴的 JSON 中缺少非空 \"mcp_token\" 字段")
		}
	} else {
		const prefix = "mcp_token="
		if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			s = strings.Trim(s, `"'`)
			s = strings.TrimSpace(s)
		}
	}
	if s == "" {
		return "", fmt.Errorf("mcp_token 不能为空")
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			return "", fmt.Errorf("mcp_token 不能包含空白字符")
		}
	}
	return s, nil
}
