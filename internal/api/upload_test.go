package api

import (
	"crypto/md5"
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestPickChunkSize(t *testing.T) {
	cases := []struct {
		size, base, want int64
	}{
		{100, 0, DefaultChunkSize},                              // 小文件用默认 4MB
		{9 * 1024 * 1024, DefaultChunkSize, DefaultChunkSize},   // 9MB → 3 片
		{5000 * 1024 * 1024, DefaultChunkSize, 8 * 1024 * 1024}, // 超过 1024 片要放大
	}
	for _, c := range cases {
		got := pickChunkSize(c.size, c.base)
		if got != c.want {
			t.Errorf("pickChunkSize(%d,%d)=%d, want %d", c.size, c.base, got, c.want)
		}
		if (c.size+got-1)/got > MaxChunks {
			t.Errorf("分片数超限: size=%d chunk=%d", c.size, got)
		}
	}
}

// TestHashFile 对照独立实现校验整文件、前 256KB、各分片 MD5。
func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	for _, size := range []int64{1, 100, 256 * 1024, 256*1024 + 1, 4*1024*1024 + 7} {
		data := make([]byte, size)
		rand.New(rand.NewSource(size)).Read(data)
		p := filepath.Join(dir, "f.bin")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		chunk := pickChunkSize(size, DefaultChunkSize)
		content, slice, blocks, err := hashFile(p, size, chunk)
		if err != nil {
			t.Fatalf("size=%d: %v", size, err)
		}
		if content != md5hex(data) {
			t.Errorf("size=%d 整文件 MD5 不对", size)
		}
		sliceLen := int64(256 * 1024)
		if size < sliceLen {
			sliceLen = size
		}
		if slice != md5hex(data[:sliceLen]) {
			t.Errorf("size=%d 前 256KB MD5 不对", size)
		}
		var wantBlocks []string
		for off := int64(0); off < size; off += chunk {
			end := off + chunk
			if end > size {
				end = size
			}
			wantBlocks = append(wantBlocks, md5hex(data[off:end]))
		}
		if len(blocks) != len(wantBlocks) {
			t.Fatalf("size=%d 分片数 %d ≠ %d", size, len(blocks), len(wantBlocks))
		}
		for i := range blocks {
			if blocks[i] != wantBlocks[i] {
				t.Errorf("size=%d 分片 %d MD5 不对", size, i)
			}
		}
	}
}

func md5hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func TestCheckErrno(t *testing.T) {
	if err := checkErrno("op", []byte(`{"errno":0}`)); err != nil {
		t.Fatalf("errno 0 应为成功: %v", err)
	}
	if err := checkErrno("op", []byte(`{"error_code":0}`)); err != nil {
		t.Fatalf("error_code 0 应为成功: %v", err)
	}
	err := checkErrno("op", []byte(`{"errno":110}`))
	ae, ok := err.(*APIError)
	if !ok || ae.Errno != 110 || !ae.AuthInvalid() {
		t.Fatalf("110 应判为令牌失效: %v", err)
	}
	err = checkErrno("op", []byte(`{"error_code":31034}`))
	ae, ok = err.(*APIError)
	if !ok || !retryableErrno(ae.Errno) {
		t.Fatalf("31034 应可重试: %v", err)
	}
	if err := checkErrno("op", []byte(`{"error":"invalid_grant"}`)); err == nil {
		t.Fatal("OAuth error 应报错")
	}
}

func TestUploadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty")
	os.WriteFile(p, []byte{}, 0o644)
	c := &Client{}
	_, err := c.UploadFile(p, "/apps/x/empty")
	if err != ErrEmptyFile {
		t.Fatalf("空文件应返回 ErrEmptyFile，得到 %v", err)
	}
}
