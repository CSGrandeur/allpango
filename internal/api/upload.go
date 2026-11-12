package api

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// DefaultChunkSize 是普通用户的分片大小：4MB。
const DefaultChunkSize int64 = 4 * 1024 * 1024

// MaxChunks 是官方规定的分片数上限。
const MaxChunks = 1024

// ErrEmptyFile：官方不允许上传空文件。备份里遇到空文件要记录并跳过，不是失败。
var ErrEmptyFile = errors.New("空文件：官方接口不允许上传，已跳过")

// UploadResult 是一次上传的结果与统计。
type UploadResult struct {
	Path           string
	Size           int64
	MD5            string // 本地算出的整文件 MD5
	ChunksTotal    int
	ChunksUploaded int
	Rapid          bool // 预上传后无需传任何分片（云端已有相同内容）
}

// fileInfo 读本地文件的大小与修改时间。
func fileInfo(path string) (int64, int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	if fi.IsDir() {
		return 0, 0, fmt.Errorf("%s 是目录，upload 只接受文件", path)
	}
	return fi.Size(), fi.ModTime().Unix(), nil
}

// hashFile 一次顺序读，算出整文件 MD5、前 256KB MD5 和各分片 MD5。
func hashFile(path string, size, chunkSize int64) (contentMD5, sliceMD5 string, blockMD5s []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", nil, err
	}
	defer f.Close()

	whole := md5.New()
	slice := md5.New()
	const sliceLen = 256 * 1024
	buf := make([]byte, 1024*1024)
	blockHash := md5.New()
	var blockLen int64
	var readTotal int64

	flushBlock := func() {
		blockMD5s = append(blockMD5s, hex.EncodeToString(blockHash.Sum(nil)))
		blockHash.Reset()
		blockLen = 0
	}

	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			whole.Write(chunk)
			// 前 256KB 校验段
			if readTotal < sliceLen {
				take := int64(n)
				if readTotal+take > sliceLen {
					take = sliceLen - readTotal
				}
				slice.Write(chunk[:take])
			}
			// 分片 MD5
			off := 0
			for off < n {
				take := int64(n - off)
				if blockLen+take > chunkSize {
					take = chunkSize - blockLen
				}
				blockHash.Write(chunk[off : off+int(take)])
				blockLen += take
				off += int(take)
				if blockLen == chunkSize {
					flushBlock()
				}
			}
			readTotal += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", "", nil, rerr
		}
	}
	if blockLen > 0 || len(blockMD5s) == 0 {
		flushBlock()
	}
	if readTotal != size {
		return "", "", nil, fmt.Errorf("读文件时大小发生变化（%d → %d），请重试", size, readTotal)
	}
	return hex.EncodeToString(whole.Sum(nil)), hex.EncodeToString(slice.Sum(nil)), blockMD5s, nil
}

// pickChunkSize 保证分片数不超过 1024：不够就翻倍放大。
func pickChunkSize(size, base int64) int64 {
	if base <= 0 {
		base = DefaultChunkSize
	}
	for (size+base-1)/base > MaxChunks {
		base *= 2
	}
	return base
}

// UploadFile 把本地文件上传到云端 remotePath（必须在应用目录内）。
// 流程：算哈希 → 预上传 → （需要时）取上传域名、传分片 → 合并。
// 覆盖同名用 rtype=3。空文件返回 ErrEmptyFile。
func (c *Client) UploadFile(localPath, remotePath string) (*UploadResult, error) {
	size, mtime, err := fileInfo(localPath)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, ErrEmptyFile
	}
	chunkSize := pickChunkSize(size, c.ChunkSize)
	contentMD5, sliceMD5, blockMD5s, err := hashFile(localPath, size, chunkSize)
	if err != nil {
		return nil, fmt.Errorf("计算文件哈希失败: %w", err)
	}
	c.logf("预上传 %s（%d 字节，%d 个分片）", remotePath, size, len(blockMD5s))
	pre, err := c.Precreate(remotePath, size, blockMD5s, contentMD5, sliceMD5, mtime)
	if err != nil {
		return nil, err
	}

	need := pre.BlockList
	rapid := false
	if len(need) == 0 {
		// 2026-09 文档：空列表等价于 [0]；return_type=2 才表示云端已有相同内容。
		// 真实行为以测试为准：这里保守地按「要传第 0 片」处理，除非 return_type=2。
		if pre.ReturnType == 2 {
			rapid = true
		} else {
			need = []int{0}
		}
	} else if pre.ReturnType == 2 {
		rapid = true
		need = nil
	}

	res := &UploadResult{Path: remotePath, Size: size, MD5: contentMD5, ChunksTotal: len(blockMD5s), Rapid: rapid}

	if !rapid && len(need) > 0 {
		hosts, err := c.LocateUpload(remotePath, pre.UploadID)
		if err != nil {
			return nil, err
		}
		if err := c.uploadChunks(localPath, remotePath, pre.UploadID, chunkSize, blockMD5s, need, hosts); err != nil {
			return nil, err
		}
		res.ChunksUploaded = len(need)
	}

	c.logf("合并 %s", remotePath)
	created, err := c.Create(remotePath, size, pre.UploadID, blockMD5s, mtime)
	if err != nil {
		return nil, err
	}
	if created.Size != size {
		return nil, fmt.Errorf("合并后云端大小 %d 与本地 %d 不一致", created.Size, size)
	}
	return res, nil
}

// uploadChunks 逐个上传需要的分片。单个分片失败时换下一个上传域名重试。
func (c *Client) uploadChunks(localPath, remotePath, uploadID string, chunkSize int64, blockMD5s []string, need []int, hosts []string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, seq := range need {
		if seq < 0 || seq >= len(blockMD5s) {
			return fmt.Errorf("预上传返回了非法分片序号 %d", seq)
		}
		buf := make([]byte, chunkSize)
		n, err := f.ReadAt(buf, int64(seq)*chunkSize)
		if err != nil && err != io.EOF {
			return fmt.Errorf("读分片 %d 失败: %w", seq, err)
		}
		data := buf[:n]
		if got := md5Hex(data); got != blockMD5s[seq] {
			return fmt.Errorf("分片 %d 的 MD5 与预上传不一致，文件可能已被修改", seq)
		}
		var lastErr error
		ok := false
		for _, host := range hosts {
			var md5Back string
			md5Back, lastErr = c.UploadChunk(host, remotePath, uploadID, seq, data)
			if lastErr == nil {
				if md5Back != blockMD5s[seq] {
					lastErr = fmt.Errorf("分片 %d 云端 MD5 与本地不一致", seq)
					continue
				}
				ok = true
				break
			}
			c.logf("分片 %d 在 %s 上传失败，换域名重试: %v", seq, host, lastErr)
		}
		if !ok {
			return fmt.Errorf("分片 %d 上传失败: %w", seq, lastErr)
		}
		c.logf("分片 %d/%d 完成", seq+1, len(blockMD5s))
	}
	return nil
}

func md5Hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}
