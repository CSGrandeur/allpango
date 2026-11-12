package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CSGrandeur/allpango/internal/api"
)

// fakeLister 返回固定云端列表。
type fakeLister struct{ entries []api.Entry }

func (f fakeLister) ListAll(dir string) ([]api.Entry, error) { return f.entries, nil }

func setupVerifyTree(t *testing.T) (string, []LocalFile) {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.bin"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "b.bin"), []byte("bb"), 0o644)
	os.WriteFile(filepath.Join(dir, "empty"), []byte{}, 0o644)
	files, _, err := WalkLocal(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	return dir, files
}

func entryFor(root string, f LocalFile, size, mtime int64) api.Entry {
	return api.Entry{Path: JoinRemote(root, f.Rel), Size: size, LocalMtime: mtime, IsDir: 0}
}

func TestVerifyAllMatched(t *testing.T) {
	dir, files := setupVerifyTree(t)
	root := "/apps/x/S"
	var entries []api.Entry
	for _, f := range files {
		if f.Size > 0 {
			entries = append(entries, entryFor(root, f, f.Size, f.Mtime))
		}
	}
	v, err := Verify(fakeLister{entries}, Options{LocalRoot: dir, RemoteRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK() || v.Matched != 2 || v.EmptySkipped != 1 {
		t.Fatalf("应全部一致: %+v", v)
	}
}

func TestVerifyMissingAndMismatch(t *testing.T) {
	dir, files := setupVerifyTree(t)
	root := "/apps/x/S"
	var entries []api.Entry
	for _, f := range files {
		if f.Rel == "a.bin" {
			entries = append(entries, entryFor(root, f, f.Size+1, f.Mtime)) // 大小不符
		}
		// sub/b.bin 缺失
	}
	v, err := Verify(fakeLister{entries}, Options{LocalRoot: dir, RemoteRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if v.OK() {
		t.Fatal("应核对不通过")
	}
	if len(v.Missing) != 1 || v.Missing[0] != "sub/b.bin" || len(v.SizeMismatch) != 1 {
		t.Fatalf("缺失/不符判定不对: %+v", v)
	}
}

func TestVerifyLegacyMtimeZero(t *testing.T) {
	dir, files := setupVerifyTree(t)
	root := "/apps/x/S"
	var entries []api.Entry
	for _, f := range files {
		if f.Size > 0 {
			entries = append(entries, entryFor(root, f, f.Size, 0)) // 旧工具没写 local_mtime
		}
	}
	v, err := Verify(fakeLister{entries}, Options{LocalRoot: dir, RemoteRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK() {
		t.Fatalf("local_mtime=0 时按大小核对即可: %+v", v)
	}
}

func TestSeedSkipsMatchedOnly(t *testing.T) {
	dir, files := setupVerifyTree(t)
	root := "/apps/x/S"
	manifest := filepath.Join(t.TempDir(), "m.json")
	var entries []api.Entry
	for _, f := range files {
		if f.Rel == "a.bin" {
			entries = append(entries, entryFor(root, f, f.Size, f.Mtime)) // 一致 → 播种
		}
		if f.Rel == "sub/b.bin" {
			entries = append(entries, entryFor(root, f, f.Size, f.Mtime+99)) // 时间不同 → 不播种
		}
	}
	opts := Options{LocalRoot: dir, RemoteRoot: root, ManifestPath: manifest}
	n, err := Seed(fakeLister{entries}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应只播种 1 个: %d", n)
	}
	m := LoadManifest(manifest)
	if !m.Unchanged("a.bin", files[0].Size, files[0].Mtime) {
		t.Fatal("a.bin 应在清单里")
	}
	// 播种后 Run：a.bin 跳过，b.bin 上传
	up := &fakeUploader{}
	stats, err := Run(up, opts)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 1 || stats.Uploaded != 1 {
		t.Fatalf("播种后应跳过1传1: %+v", stats)
	}
}
func TestSeedLegacySizeOnly(t *testing.T) {
	dir, files := setupVerifyTree(t)
	root := "/apps/x/S"
	manifest := filepath.Join(t.TempDir(), "m.json")
	var entries []api.Entry
	for _, f := range files {
		if f.Size > 0 {
			// 旧工具语义：local_mtime 是上传时刻，与本地 mtime 不同
			entries = append(entries, entryFor(root, f, f.Size, f.Mtime+88888))
		}
	}
	opts := Options{LocalRoot: dir, RemoteRoot: root, ManifestPath: manifest}
	// 严格播种：0
	n, err := Seed(fakeLister{entries}, opts)
	if err != nil || n != 0 {
		t.Fatalf("严格播种应为 0: %d %v", n, err)
	}
	// legacy 播种：2
	n, err = SeedLegacy(fakeLister{entries}, opts)
	if err != nil || n != 2 {
		t.Fatalf("legacy 播种应为 2: %d %v", n, err)
	}
	up := &fakeUploader{}
	stats, err := Run(up, opts)
	if err != nil || stats.Skipped != 2 || stats.Uploaded != 0 {
		t.Fatalf("播种后应全跳过: %+v %v", stats, err)
	}
}

func TestVerifyMtimeSeparate(t *testing.T) {
	dir, files := setupVerifyTree(t)
	root := "/apps/x/S"
	var entries []api.Entry
	for _, f := range files {
		if f.Size > 0 {
			entries = append(entries, entryFor(root, f, f.Size, f.Mtime+7777))
		}
	}
	v, err := Verify(fakeLister{entries}, Options{LocalRoot: dir, RemoteRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.SizeMismatch) != 0 || len(v.MtimeMismatch) != 2 {
		t.Fatalf("mtime 不符应单列: %+v", v)
	}
	if v.OK() {
		t.Fatal("默认模式 mtime 不符仍应不通过")
	}
}

func TestWalkLocalIgnoresStateFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "data.bin"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, ".allpango_manifest.json"), []byte("{}"), 0o644)
	files, _, err := WalkLocal(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Rel != "data.bin" {
		t.Fatalf("状态文件应被忽略: %+v", files)
	}
}
