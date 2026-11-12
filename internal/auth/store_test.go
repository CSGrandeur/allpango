package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "token.json")
	tf := &TokenFile{AccessToken: "a", RefreshToken: "r", ExpiresIn: 100, AccessTokenExpiry: 200}
	if err := tf.Save(p); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("令牌文件权限应为 0600，得到 %o", fi.Mode().Perm())
	}
	got, err := LoadTokenFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "a" || got.RefreshToken != "r" || got.AccessTokenExpiry != 200 {
		t.Fatalf("往返不一致: %+v", got)
	}
}

func TestLoadTokenMissing(t *testing.T) {
	tf, err := LoadTokenFile(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	if tf.AccessToken != "" {
		t.Fatal("缺失文件应返回零值")
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Unix(1000000, 0)
	cases := []struct {
		name string
		tf   TokenFile
		want bool
	}{
		{"无令牌", TokenFile{}, true},
		{"只有access", TokenFile{AccessToken: "a"}, true},
		{"无到期信息先用着", TokenFile{AccessToken: "a", RefreshToken: "r"}, false},
		{"未到期", TokenFile{AccessToken: "a", RefreshToken: "r", AccessTokenExpiry: now.Unix() + 7200}, false},
		{"临近到期", TokenFile{AccessToken: "a", RefreshToken: "r", AccessTokenExpiry: now.Unix() + 1800}, true},
		{"已过期", TokenFile{AccessToken: "a", RefreshToken: "r", AccessTokenExpiry: now.Unix() - 1}, true},
	}
	for _, c := range cases {
		if got := c.tf.NeedsRefresh(now); got != c.want {
			t.Errorf("%s: NeedsRefresh=%v want %v", c.name, got, c.want)
		}
	}
}

func TestApplyRefreshRotatesToken(t *testing.T) {
	now := time.Unix(1000000, 0)
	tf := &TokenFile{AccessToken: "old-a", RefreshToken: "old-r"}
	tf.ApplyRefresh("new-a", "new-r", 2592000, "basic netdisk", now)
	if tf.RefreshToken != "new-r" {
		t.Fatal("refresh token 应轮换")
	}
	if tf.AccessTokenExpiry != now.Unix()+2592000 {
		t.Fatal("到期时刻推算不对")
	}
	// 响应不带新 refresh token 时保留旧值
	tf.ApplyRefresh("newer-a", "", 100, "", now)
	if tf.RefreshToken != "new-r" {
		t.Fatal("空 refresh token 不应清掉旧值")
	}
}

func TestLoadCredentials(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cred.json")
	os.WriteFile(p, []byte(`{"app_key":"k","app_secret":"s"}`), 0o600)
	c, err := LoadCredentials(p)
	if err != nil || c.AppKey != "k" || c.AppSecret != "s" {
		t.Fatalf("读取凭证失败: %v %+v", err, c)
	}
	os.WriteFile(p, []byte(`{"app_key":"k"}`), 0o600)
	if _, err := LoadCredentials(p); err == nil {
		t.Fatal("缺 app_secret 应报错")
	}
}
