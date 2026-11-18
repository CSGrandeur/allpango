package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndSelectRemotes(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "allpango.json")
	raw := `{
  "remotes": {
    "baidu": {
      "provider": "baidu",
      "cred": "secrets/b-cred.json",
      "token": "secrets/b-tok.json",
      "path": "/apps/myapp/backup",
      "manifest": "mf/baidu.json"
    },
    "weiyun": {
      "provider": "weiyun",
      "token": "secrets/w-tok.json",
      "path": "/备份/backup",
      "manifest": "mf/weiyun.json",
      "parallel": 2
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if got := cfg.Remotes["baidu"].Cred; got != filepath.Join(dir, "secrets/b-cred.json") {
		t.Fatalf("相对路径未按配置目录展开: %s", got)
	}
	if got := cfg.Remotes["weiyun"].Manifest; got != filepath.Join(dir, "mf/weiyun.json") {
		t.Fatalf("manifest 路径未展开: %s", got)
	}

	sel, err := cfg.Select("weiyun,baidu", false)
	if err != nil || len(sel) != 2 || sel[0].Name != "weiyun" || sel[1].Name != "baidu" {
		t.Fatalf("Select 保序失败: %+v %v", sel, err)
	}
	all, err := cfg.Select("", true)
	if err != nil || len(all) != 2 || all[0].Name != "baidu" {
		t.Fatalf("Select --all 失败: %+v %v", all, err)
	}
	if _, err := cfg.Select("", false); err == nil {
		t.Fatal("多网盘省略 -r 应报错")
	}
	if _, err := cfg.Select("baidu", true); err == nil {
		t.Fatal("--all 与 -r 混用应报错")
	}
}

func TestLoadRejectsInlineSecretsAndSharedManifest(t *testing.T) {
	dir := t.TempDir()
	badSecret := filepath.Join(dir, "bad-secret.json")
	os.WriteFile(badSecret, []byte(`{"remotes":{"w":{"provider":"weiyun","mcp_token":"x"}}}`), 0o600)
	if _, err := Load(badSecret); err == nil || !strings.Contains(err.Error(), "不能直接存放密钥") {
		t.Fatalf("内联密钥应拦截: %v", err)
	}

	dupManifest := filepath.Join(dir, "dup-mf.json")
	os.WriteFile(dupManifest, []byte(`{
  "remotes": {
    "a": {"provider": "weiyun", "token": "t1.json", "manifest": "same.json"},
    "b": {"provider": "weiyun", "token": "t2.json", "manifest": "./same.json"}
  }
}`), 0o600)
	if _, err := Load(dupManifest); err == nil || !strings.Contains(err.Error(), "独立清单") {
		t.Fatalf("共用 manifest 应拦截: %v", err)
	}

	weiyunWithCred := filepath.Join(dir, "wy-cred.json")
	os.WriteFile(weiyunWithCred, []byte(`{"remotes":{"w":{"provider":"weiyun","cred":"c.json","token":"t.json"}}}`), 0o600)
	if _, err := Load(weiyunWithCred); err == nil {
		t.Fatal("weiyun 带 cred 应拦截")
	}
}

func TestDetectProviderFromTokenFile(t *testing.T) {
	dir := t.TempDir()
	wy := filepath.Join(dir, "wy.json")
	os.WriteFile(wy, []byte(`{"mcp_token":"x"}`), 0o600)
	if p, err := DetectProviderFromTokenFile(wy); err != nil || p != ProviderWeiyun {
		t.Fatalf("微云识别失败: %q %v", p, err)
	}

	bd := filepath.Join(dir, "bd.json")
	os.WriteFile(bd, []byte(`{"refresh_token":"r"}`), 0o600)
	if p, err := DetectProviderFromTokenFile(bd); err != nil || p != ProviderBaidu {
		t.Fatalf("百度识别失败: %q %v", p, err)
	}

	if p, err := DetectProviderFromTokenFile(filepath.Join(dir, "missing.json")); err != nil || p != "" {
		t.Fatalf("缺失文件应返回空: %q %v", p, err)
	}
}
