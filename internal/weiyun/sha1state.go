// Package weiyun 实现腾讯微云官方 MCP 客户端。
// 只做备份需要的能力：令牌校验、列目录、建目录、两阶段上传。没有删除与移动。
package weiyun

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/bits"
	"os"

	"github.com/CSGrandeur/allpango/internal/api"
)

// BlockSize 是微云预上传分块与分片的固定大小（512 KB）。
const BlockSize int64 = 524288

// UploadParams 保存微云预上传所需的校验与摘要参数。
type UploadParams struct {
	Filename     string
	FileSize     int64
	Mtime        int64
	FileSHA      string
	FileMD5      string
	BlockSHAList []string
	CheckSHA     string
	CheckData    string
}

// sha1Stream 维护未填充的 SHA-1 内部寄存器，以输出微云分块所需的中间状态。
type sha1Stream struct {
	h   [5]uint32
	x   [64]byte
	nx  int
	len uint64
}

func newSHA1Stream() *sha1Stream {
	return &sha1Stream{
		h: [5]uint32{0x67452301, 0xEFCDAB89, 0x98BADCFE, 0x10325476, 0xC3D2E1F0},
	}
}

func (d *sha1Stream) Write(p []byte) (int, error) {
	nn := len(p)
	d.len += uint64(nn)
	if d.nx > 0 {
		n := copy(d.x[d.nx:], p)
		d.nx += n
		if d.nx == 64 {
			d.block(d.x[:])
			d.nx = 0
		}
		p = p[n:]
	}
	for len(p) >= 64 {
		d.block(p[:64])
		p = p[64:]
	}
	if len(p) > 0 {
		d.nx = copy(d.x[:], p)
	}
	return nn, nil
}

func (d *sha1Stream) block(c []byte) {
	var w [80]uint32
	for i := 0; i < 16; i++ {
		w[i] = binary.BigEndian.Uint32(c[i*4 : (i+1)*4])
	}
	for i := 16; i < 80; i++ {
		w[i] = bits.RotateLeft32(w[i-3]^w[i-8]^w[i-14]^w[i-16], 1)
	}
	a, b, cc, dd, e := d.h[0], d.h[1], d.h[2], d.h[3], d.h[4]
	for i := 0; i < 80; i++ {
		var f, k uint32
		switch {
		case i < 20:
			f, k = (b&cc)|((^b)&dd), 0x5A827999
		case i < 40:
			f, k = b^cc^dd, 0x6ED9EBA1
		case i < 60:
			f, k = (b&cc)|(b&dd)|(cc&dd), 0x8F1BBCDC
		default:
			f, k = b^cc^dd, 0xCA62C1D6
		}
		tmp := bits.RotateLeft32(a, 5) + f + e + k + w[i]
		e, dd, cc, b, a = dd, cc, bits.RotateLeft32(b, 30), a, tmp
	}
	d.h[0] += a
	d.h[1] += b
	d.h[2] += cc
	d.h[3] += dd
	d.h[4] += e
}

// StateLEHex 将当前 5 个 SHA-1 寄存器按小端序输出为 40 位十六进制串。
func (d *sha1Stream) StateLEHex() (string, error) {
	if d.nx != 0 {
		return "", fmt.Errorf("SHA1 中间状态存在 %d 字节未对齐", d.nx)
	}
	var out [20]byte
	for i, v := range d.h {
		binary.LittleEndian.PutUint32(out[i*4:(i+1)*4], v)
	}
	return hex.EncodeToString(out[:]), nil
}

// SumHex 输出完整标准 SHA-1 十六进制摘要（大端序）。
func (d *sha1Stream) SumHex() string {
	tmp := *d
	bitLen := tmp.len * 8
	var pad [64]byte
	pad[0] = 0x80
	if tmp.nx < 56 {
		_, _ = tmp.Write(pad[:56-tmp.nx])
	} else {
		_, _ = tmp.Write(pad[:64+56-tmp.nx])
	}
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], bitLen)
	_, _ = tmp.Write(lenBuf[:])
	var out [20]byte
	for i, v := range tmp.h {
		binary.BigEndian.PutUint32(out[i*4:(i+1)*4], v)
	}
	return hex.EncodeToString(out[:])
}

// CalcUploadParams 顺序流式计算微云预上传所需的分块 SHA1 状态、尾块校验与整文件摘要。
func CalcUploadParams(filePath string) (*UploadParams, error) {
	fi, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s 是目录，upload 只接受文件", filePath)
	}
	size := fi.Size()
	if size == 0 {
		return nil, api.ErrEmptyFile
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	lastBlock := size % BlockSize
	if lastBlock == 0 {
		lastBlock = BlockSize
	}
	checkBlock := lastBlock % 128
	if checkBlock == 0 {
		checkBlock = 128
	}
	before := size - lastBlock

	sha1 := newSHA1Stream()
	wholeMD5 := md5.New()
	blockCount := int((size + BlockSize - 1) / BlockSize)
	blockSHA := make([]string, 0, blockCount)

	buf := make([]byte, BlockSize)
	var readTotal int64
	for readTotal < before {
		if _, err := io.ReadFull(f, buf); err != nil {
			return nil, fmt.Errorf("读文件失败: %w", err)
		}
		_, _ = sha1.Write(buf)
		_, _ = wholeMD5.Write(buf)
		st, err := sha1.StateLEHex()
		if err != nil {
			return nil, err
		}
		blockSHA = append(blockSHA, st)
		readTotal += BlockSize
	}

	midLen := int(lastBlock - checkBlock)
	if midLen > 0 {
		midBuf := buf[:midLen]
		if _, err := io.ReadFull(f, midBuf); err != nil {
			return nil, fmt.Errorf("读文件失败: %w", err)
		}
		_, _ = sha1.Write(midBuf)
		_, _ = wholeMD5.Write(midBuf)
		readTotal += int64(midLen)
	}
	checkSHA, err := sha1.StateLEHex()
	if err != nil {
		return nil, err
	}

	tail := make([]byte, checkBlock)
	if _, err := io.ReadFull(f, tail); err != nil {
		return nil, fmt.Errorf("读文件尾块失败: %w", err)
	}
	_, _ = sha1.Write(tail)
	_, _ = wholeMD5.Write(tail)
	readTotal += checkBlock

	// 确认没有多余数据（防止扫描后文件变长）。
	var extra [1]byte
	if n, err := f.Read(extra[:]); n > 0 || (err != nil && err != io.EOF) {
		return nil, fmt.Errorf("读文件时大小发生变化，请重试")
	}
	if readTotal != size {
		return nil, fmt.Errorf("读文件时大小发生变化（%d → %d），请重试", size, readTotal)
	}

	fileSHA := sha1.SumHex()
	blockSHA = append(blockSHA, fileSHA)
	return &UploadParams{
		Filename:     fi.Name(),
		FileSize:     size,
		Mtime:        fi.ModTime().Unix(),
		FileSHA:      fileSHA,
		FileMD5:      hex.EncodeToString(wholeMD5.Sum(nil)),
		BlockSHAList: blockSHA,
		CheckSHA:     checkSHA,
		CheckData:    base64.StdEncoding.EncodeToString(tail),
	}, nil
}
