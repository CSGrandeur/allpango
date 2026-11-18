package weiyun

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CSGrandeur/allpango/internal/api"
)

func TestCalcUploadParamsVectors(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		size     int
		blocks   int
		fileSHA  string
		fileMD5  string
		checkSHA string
		block0   string
	}{
		{1, 1, "5d1be7e9dda1ee8896be5b7e34a85ee16452a7b4", "89e74e640b8c46257a29de0616794d5d", "0123456789abcdeffedcba9876543210f0e1d2c3", "5d1be7e9dda1ee8896be5b7e34a85ee16452a7b4"},
		{64, 1, "39a0d8b645ad85f1f976731ed112ac9455e28b78", "b6bf87c24b1bc334e2541387a92b981b", "0123456789abcdeffedcba9876543210f0e1d2c3", "39a0d8b645ad85f1f976731ed112ac9455e28b78"},
		{127, 1, "bebc42d2d3d1e5fb8ad8895c2dcef2d68a6c279a", "27670172396247fa5f68637f13f8933d", "0123456789abcdeffedcba9876543210f0e1d2c3", "bebc42d2d3d1e5fb8ad8895c2dcef2d68a6c279a"},
		{128, 1, "0060f2a7e34b6e4d459f560197ef93243732a400", "3e85b70ffc8df5c735ecf2a8f14f1bee", "0123456789abcdeffedcba9876543210f0e1d2c3", "0060f2a7e34b6e4d459f560197ef93243732a400"},
		{129, 1, "3a16082d1bf09b604907ec6908b9893ca3e937c0", "e45ecac9930fe93de302f98ba673ba36", "e08491e5e6f610416981a679ac55d3222256952e", "3a16082d1bf09b604907ec6908b9893ca3e937c0"},
		{524288, 1, "f64cad1321b3b25baa16ab48b0783b49dd4b0a22", "f161f3c7e5988ed42e3db7b5b2497f0e", "e878c4fb0a8a79df80359255185d769806fc11c4", "f64cad1321b3b25baa16ab48b0783b49dd4b0a22"},
		{524289, 2, "43c2d453914fe95007728161a723d0f1ef637b5f", "a4acb06d3378e3fb11707d817028ac3c", "a0ac5bf5d8a544edbe3605aa92685399170c423f", "a0ac5bf5d8a544edbe3605aa92685399170c423f"},
		{1048876, 3, "979195e8599d6b8522dc35ef184f3072568015c2", "a729a7c039067e17ff0a30368f88d307", "09f2362d51830d61ae384ab558f14043244553f8", "a0ac5bf5d8a544edbe3605aa92685399170c423f"},
	}

	for _, tc := range cases {
		data := make([]byte, tc.size)
		for i := range data {
			data[i] = byte((i*31 + 7) & 0xff)
		}
		p := filepath.Join(dir, "vec.bin")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := CalcUploadParams(p)
		if err != nil {
			t.Fatalf("size=%d: %v", tc.size, err)
		}
		if len(got.BlockSHAList) != tc.blocks {
			t.Fatalf("size=%d blocks=%d, want %d", tc.size, len(got.BlockSHAList), tc.blocks)
		}
		if got.FileSHA != tc.fileSHA {
			t.Errorf("size=%d file_sha=%s, want %s", tc.size, got.FileSHA, tc.fileSHA)
		}
		if got.FileMD5 != tc.fileMD5 {
			t.Errorf("size=%d file_md5=%s, want %s", tc.size, got.FileMD5, tc.fileMD5)
		}
		if got.CheckSHA != tc.checkSHA {
			t.Errorf("size=%d check_sha=%s, want %s", tc.size, got.CheckSHA, tc.checkSHA)
		}
		if got.BlockSHAList[0] != tc.block0 {
			t.Errorf("size=%d b0=%s, want %s", tc.size, got.BlockSHAList[0], tc.block0)
		}
		if got.BlockSHAList[len(got.BlockSHAList)-1] != tc.fileSHA {
			t.Errorf("size=%d last block != file_sha", tc.size)
		}
	}
}

func TestCalcUploadParamsEmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := CalcUploadParams(p)
	if !errors.Is(err, api.ErrEmptyFile) {
		t.Fatalf("空文件应返回 ErrEmptyFile，得到 %v", err)
	}
}

func TestCleanPath(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"/", "/", false},
		{"全部", "/", false},
		{"全部/备份/backup", "/备份/backup", false},
		{"备份\\2026\\db", "/备份/2026/db", false},
		{"/备份/全部/子目录", "/备份/全部/子目录", false},
		{"", "", true},
		{"/备份/../x", "", true},
		{"/备份/./x", "", true},
	}
	for _, tc := range cases {
		got, err := CleanPath(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("CleanPath(%q) 应报错", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("CleanPath(%q) = (%q, %v), want (%q, nil)", tc.in, got, err, tc.want)
		}
	}
}

func TestTokenRoundtripAndParseInput(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "wy.json")
	tf := &TokenFile{MCPToken: "tok-1", EnvID: "e1"}
	if err := tf.Save(p); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("权限应为 0600，得到 %o", fi.Mode().Perm())
	}
	loaded, err := LoadTokenFile(p)
	if err != nil || loaded.MCPToken != "tok-1" || loaded.EnvID != "e1" {
		t.Fatalf("往返不一致: %+v %v", loaded, err)
	}

	for _, raw := range []string{
		"tok-2",
		"  mcp_token=tok-2 \n",
		`{"mcp_token": "tok-2"}`,
	} {
		got, err := ParseTokenInput(raw)
		if err != nil || got != "tok-2" {
			t.Errorf("ParseTokenInput(%q) = (%q, %v)", raw, got, err)
		}
	}
	if _, err := ParseTokenInput("bad secret value"); err == nil || strings.Contains(err.Error(), "bad") {
		t.Fatalf("含空格输入应报错且不回显原值: %v", err)
	}
}
