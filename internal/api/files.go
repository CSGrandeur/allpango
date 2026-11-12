package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Quota 是网盘容量。
type Quota struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
}

// GetQuota 查询容量，也用来验证令牌可用。
func (c *Client) GetQuota() (*Quota, error) {
	ep := c.Endpoints.withDefaults()
	var out Quota
	err := c.call("查询容量", func() (*http.Request, error) {
		q := url.Values{}
		q.Set("access_token", c.accessToken())
		q.Set("checkfree", "1")
		q.Set("checkexpire", "1")
		return http.NewRequest(http.MethodGet, queryURL(ep.Pan, "/api/quota", q), nil)
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Entry 是目录里的一条文件或子目录。
type Entry struct {
	Path           string `json:"path"`
	ServerFilename string `json:"server_filename"`
	Size           int64  `json:"size"`
	IsDir          int    `json:"isdir"`
	ServerMtime    int64  `json:"server_mtime"`
	LocalMtime     int64  `json:"local_mtime"`
	MD5            string `json:"md5"` // 云端哈希，不是文件真实 MD5
	FsID           uint64 `json:"fs_id"`
}

// ListDir 列出一个目录下的全部条目（自动翻页）。
// 目录不存在时返回 *APIError（errno -9），调用者可据此决定建目录。
func (c *Client) ListDir(dir string) ([]Entry, error) {
	ep := c.Endpoints.withDefaults()
	const limit = 1000
	var all []Entry
	for start := 0; ; start += limit {
		var page struct {
			List []Entry `json:"list"`
		}
		s := start
		err := c.call("列目录", func() (*http.Request, error) {
			q := url.Values{}
			q.Set("method", "list")
			q.Set("access_token", c.accessToken())
			q.Set("dir", dir)
			q.Set("order", "name")
			q.Set("start", strconv.Itoa(s))
			q.Set("limit", strconv.Itoa(limit))
			return http.NewRequest(http.MethodGet, queryURL(ep.Pan, "/rest/2.0/xpan/file", q), nil)
		}, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page.List...)
		if len(page.List) < limit {
			return all, nil
		}
	}
}

// ListAll 递归列出一个目录下的全部文件与子目录（自动翻页）。
// 用官方 listall 接口；has_more=1 时以返回的 cursor 作为下一页 start。
// 频控（31034）由 call 层退避重试；页间主动限速，避免超过每分钟 8–10 次的建议。
func (c *Client) ListAll(dir string) ([]Entry, error) {
	ep := c.Endpoints.withDefaults()
	var all []Entry
	start := 0
	for page := 0; ; page++ {
		if page > 0 {
			time.Sleep(6 * time.Second)
		}
		c.logf("递归列目录 第 %d 页（已累计 %d 条）", page+1, len(all))
		var resp struct {
			HasMore int     `json:"has_more"`
			Cursor  int     `json:"cursor"`
			List    []Entry `json:"list"`
		}
		s := start
		err := c.call("递归列目录", func() (*http.Request, error) {
			q := url.Values{}
			q.Set("method", "listall")
			q.Set("access_token", c.accessToken())
			q.Set("path", dir)
			q.Set("recursion", "1")
			q.Set("order", "name")
			q.Set("start", strconv.Itoa(s))
			q.Set("limit", "1000")
			return http.NewRequest(http.MethodGet, queryURL(ep.Pan, "/rest/2.0/xpan/multimedia", q), nil)
		}, &resp)
		if err != nil {
			return nil, err
		}
		all = append(all, resp.List...)
		if resp.HasMore != 1 {
			return all, nil
		}
		start = resp.Cursor
	}
}

// Mkdir 创建目录。已存在（errno -8）视为成功。
func (c *Client) Mkdir(path string) error {
	ep := c.Endpoints.withDefaults()
	err := c.call("创建目录", func() (*http.Request, error) {
		form := url.Values{}
		form.Set("path", path)
		form.Set("isdir", "1")
		form.Set("rtype", "0")
		q := url.Values{}
		q.Set("method", "create")
		q.Set("access_token", c.accessToken())
		r, err := http.NewRequest(http.MethodPost, queryURL(ep.Pan, "/rest/2.0/xpan/file", q), strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r, nil
	}, nil)
	if err != nil {
		if ae, ok := err.(*APIError); ok && ae.Errno == -8 {
			return nil
		}
		return err
	}
	return nil
}

// PrecreateResult 是预上传的响应。
// BlockList 是需要上传的分片序号（从 0 开始）。
// 2026-09 版文档：空列表等价于 [0]；是否免传看 ReturnType（2 表示云端已有相同内容）。
// 真实行为要用小文件验证，不能只凭空列表断定秒传。
type PrecreateResult struct {
	Path       string `json:"path"`
	UploadID   string `json:"uploadid"`
	ReturnType int    `json:"return_type"`
	BlockList  []int  `json:"block_list"`
}

// Precreate 预上传。blockMD5s 是各分片 MD5（32 位小写）按序排列；
// contentMD5 是整文件 MD5，sliceMD5 是前 256KB 的 MD5。
func (c *Client) Precreate(path string, size int64, blockMD5s []string, contentMD5, sliceMD5 string, mtime int64) (*PrecreateResult, error) {
	ep := c.Endpoints.withDefaults()
	blockJSON, err := json.Marshal(blockMD5s)
	if err != nil {
		return nil, err
	}
	var out PrecreateResult
	err = c.call("预上传", func() (*http.Request, error) {
		form := url.Values{}
		form.Set("path", path)
		form.Set("size", strconv.FormatInt(size, 10))
		form.Set("isdir", "0")
		form.Set("autoinit", "1")
		form.Set("rtype", "3") // 覆盖同名，不另存 name(1)
		form.Set("block_list", string(blockJSON))
		form.Set("content-md5", contentMD5)
		form.Set("slice-md5", sliceMD5)
		form.Set("local_ctime", strconv.FormatInt(mtime, 10))
		form.Set("local_mtime", strconv.FormatInt(mtime, 10))
		q := url.Values{}
		q.Set("method", "precreate")
		q.Set("access_token", c.accessToken())
		r, err := http.NewRequest(http.MethodPost, queryURL(ep.Pan, "/rest/2.0/xpan/file", q), strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r, nil
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.UploadID == "" {
		return nil, fmt.Errorf("预上传: 响应缺少 uploadid")
	}
	return &out, nil
}

// LocateUpload 获取当前上传任务的上传域名。必须在预上传之后、分片上传之前调用，
// path 和 uploadid 必须与预上传一致。返回可用的 HTTPS 地址列表（含协议）。
func (c *Client) LocateUpload(path, uploadID string) ([]string, error) {
	ep := c.Endpoints.withDefaults()
	var out struct {
		Host    string `json:"host"`
		Servers []struct {
			Server string `json:"server"`
		} `json:"servers"`
		BakServers []struct {
			Server string `json:"server"`
		} `json:"bak_servers"`
	}
	err := c.call("获取上传域名", func() (*http.Request, error) {
		q := url.Values{}
		q.Set("method", "locateupload")
		q.Set("appid", "250528")
		q.Set("access_token", c.accessToken())
		q.Set("path", path)
		q.Set("uploadid", uploadID)
		q.Set("upload_version", "2.0")
		return http.NewRequest(http.MethodGet, queryURL(ep.PCS, "/rest/2.0/pcs/file", q), nil)
	}, &out)
	if err != nil {
		return nil, err
	}
	var hosts []string
	add := func(s string) {
		if s == "" {
			return
		}
		if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
			s = "https://" + s
		}
		hosts = append(hosts, strings.TrimRight(s, "/"))
	}
	for _, s := range out.Servers {
		add(s.Server)
	}
	add(out.Host)
	for _, s := range out.BakServers {
		add(s.Server)
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("获取上传域名: 响应没有可用地址")
	}
	return hosts, nil
}

// UploadChunk 上传一个分片到指定上传域名，返回云端记录的分片 MD5。
// host 必须来自 LocateUpload。data 是该分片的完整字节。
func (c *Client) UploadChunk(host, path, uploadID string, partSeq int, data []byte) (string, error) {
	op := fmt.Sprintf("上传分片 %d", partSeq)
	var out struct {
		MD5 string `json:"md5"`
	}
	err := c.call(op, func() (*http.Request, error) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		fw, err := w.CreateFormFile("file", "chunk")
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(data); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		q := url.Values{}
		q.Set("method", "upload")
		q.Set("access_token", c.accessToken())
		q.Set("type", "tmpfile")
		q.Set("path", path)
		q.Set("uploadid", uploadID)
		q.Set("partseq", strconv.Itoa(partSeq))
		r, err := http.NewRequest(http.MethodPost, queryURL(host, "/rest/2.0/pcs/superfile2", q), &buf)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", w.FormDataContentType())
		return r, nil
	}, &out)
	if err != nil {
		return "", err
	}
	if out.MD5 == "" {
		return "", fmt.Errorf("%s: 响应缺少 md5", op)
	}
	return out.MD5, nil
}

// CreateResult 是合并（创建文件）的响应。
type CreateResult struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	FsID  uint64 `json:"fs_id"`
	MD5   string `json:"md5"`
	IsDir int    `json:"isdir"`
	Mtime int64  `json:"mtime"`
}

// Create 合并分片生成文件。blockMD5s 必须与预上传一致且按序。
func (c *Client) Create(path string, size int64, uploadID string, blockMD5s []string, mtime int64) (*CreateResult, error) {
	ep := c.Endpoints.withDefaults()
	blockJSON, err := json.Marshal(blockMD5s)
	if err != nil {
		return nil, err
	}
	var out CreateResult
	err = c.call("创建文件", func() (*http.Request, error) {
		form := url.Values{}
		form.Set("path", path)
		form.Set("size", strconv.FormatInt(size, 10))
		form.Set("isdir", "0")
		form.Set("uploadid", uploadID)
		form.Set("rtype", "3")
		form.Set("block_list", string(blockJSON))
		form.Set("local_ctime", strconv.FormatInt(mtime, 10))
		form.Set("local_mtime", strconv.FormatInt(mtime, 10))
		q := url.Values{}
		q.Set("method", "create")
		q.Set("access_token", c.accessToken())
		r, err := http.NewRequest(http.MethodPost, queryURL(ep.Pan, "/rest/2.0/xpan/file", q), strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r, nil
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
