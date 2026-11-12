package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestRoundtripAndUnchanged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.json")
	m := LoadManifest(p) // 不存在 → 空清单
	if len(m.Files) != 0 {
		t.Fatal("缺失清单应为空")
	}
	m.Mark("a/b.bin", 100, 12345, "md5x", "/apps/x/a/b.bin")
	if err := m.Save(p); err != nil {
		t.Fatal(err)
	}
	m2 := LoadManifest(p)
	if !m2.Unchanged("a/b.bin", 100, 12345) {
		t.Fatal("相同大小+mtime 应判未变")
	}
	if m2.Unchanged("a/b.bin", 101, 12345) {
		t.Fatal("大小变了应判已变")
	}
	if m2.Unchanged("a/b.bin", 100, 99999) {
		t.Fatal("mtime 变了应判已变")
	}
	if m2.Unchanged("missing", 1, 1) {
		t.Fatal("不存在的文件不应判未变")
	}
}

func TestManifestCorruptFallsBack(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.json")
	os.WriteFile(p, []byte("{坏掉的JSON"), 0o644)
	m := LoadManifest(p)
	if len(m.Files) != 0 {
		t.Fatal("损坏清单应退回空清单而不是报错")
	}
}

func TestWalkLocal(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bb"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "c.txt"), []byte("ccc"), 0o644)
	manifest := filepath.Join(dir, "manifest.json")
	os.WriteFile(manifest, []byte("{}"), 0o644)

	files, _, err := WalkLocal(dir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("应扫到 3 个文件（排除清单），得到 %d", len(files))
	}
	// 排序：a.txt, b.txt, sub/c.txt
	if files[0].Rel != "a.txt" || files[2].Rel != "sub/c.txt" {
		t.Fatalf("排序或相对路径不对: %+v", files)
	}
}

func TestJoinRemote(t *testing.T) {
	if got := JoinRemote("/apps/x/SYS/", "sub/a.bin"); got != "/apps/x/SYS/sub/a.bin" {
		t.Fatalf("拼接不对: %s", got)
	}
}
func TestWalkLocalPermissionSkipped(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 下权限不生效")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("x"), 0o644)
	locked := filepath.Join(dir, "locked")
	os.MkdirAll(locked, 0o755)
	os.WriteFile(filepath.Join(locked, "secret"), []byte("s"), 0o644)
	os.Chmod(locked, 0o000)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	files, skipped, err := WalkLocal(dir, "")
	if err != nil {
		t.Fatalf("无权限目录应跳过而不是报错: %v", err)
	}
	if len(files) != 1 || files[0].Rel != "ok.txt" {
		t.Fatalf("应只读到 ok.txt: %+v", files)
	}
	if len(skipped) != 1 {
		t.Fatalf("应记录 1 个跳过路径: %v", skipped)
	}
}
