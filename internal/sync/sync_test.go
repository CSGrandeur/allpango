package sync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/CSGrandeur/allpango/internal/api"
)

// fakeUploader 记录调用，可注入失败。
type fakeUploader struct {
	calls   []string
	failOn  string
	rapidOn string
}

func (f *fakeUploader) UploadFile(local, remote string) (*api.UploadResult, error) {
	f.calls = append(f.calls, remote)
	if remote == f.failOn {
		return nil, errors.New("注入的失败")
	}
	return &api.UploadResult{Path: remote, Size: 1, MD5: "m", ChunksTotal: 1, Rapid: remote == f.rapidOn}, nil
}

func setupTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.bin"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.bin"), []byte("bb"), 0o644)
	os.WriteFile(filepath.Join(dir, "empty"), []byte{}, 0o644)
	return dir
}

func TestRunUploadsAndMarks(t *testing.T) {
	dir := setupTree(t)
	manifest := filepath.Join(t.TempDir(), "m.json")
	up := &fakeUploader{}
	stats, err := Run(up, Options{LocalRoot: dir, RemoteRoot: "/apps/x/S", ManifestPath: manifest, Parallel: 2})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Uploaded != 2 || stats.EmptySkipped != 1 || stats.Failed != 0 {
		t.Fatalf("统计不对: %+v", stats)
	}
	m := LoadManifest(manifest)
	if len(m.Files) != 2 {
		t.Fatalf("清单应记录 2 个文件: %+v", m.Files)
	}
	// 第二次全部跳过
	up2 := &fakeUploader{}
	stats2, err := Run(up2, Options{LocalRoot: dir, RemoteRoot: "/apps/x/S", ManifestPath: manifest})
	if err != nil {
		t.Fatal(err)
	}
	if stats2.Uploaded != 0 || stats2.Skipped != 2 || len(up2.calls) != 0 {
		t.Fatalf("第二次应全跳过: %+v calls=%v", stats2, up2.calls)
	}
}

func TestRunFailureReportsButContinues(t *testing.T) {
	dir := setupTree(t)
	up := &fakeUploader{failOn: "/apps/x/S/a.bin"}
	stats, err := Run(up, Options{LocalRoot: dir, RemoteRoot: "/apps/x/S", Parallel: 1})
	if err == nil {
		t.Fatal("有失败应返回错误")
	}
	if stats.Failed != 1 || stats.Uploaded != 1 {
		t.Fatalf("失败 1 个但其余应传完: %+v", stats)
	}
}

func TestRunRequiresPaths(t *testing.T) {
	if _, err := Run(&fakeUploader{}, Options{}); err == nil {
		t.Fatal("缺路径应报错")
	}
	if _, err := Run(&fakeUploader{}, Options{LocalRoot: "/x", RemoteRoot: "relative"}); err == nil {
		t.Fatal("云端相对路径应报错")
	}
}
