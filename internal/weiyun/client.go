package weiyun

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CSGrandeur/allpango/internal/api"
)

const (
	// DefaultMCPURL 是微云官方 MCP HTTP 入口。
	DefaultMCPURL = "https://www.weiyun.com/api/v3/mcpserver"
	// ListLimit 是微云单页列目录允许的最大条目数。
	ListLimit = 50
	// ClientVersion 上报于 MCP req_header。
	ClientVersion = "1.0.0"
)

// Client 是微云 MCP 客户端。仅提供列目录、建目录与上传能力，不含删除与移动。
type Client struct {
	MCPToken   string
	EnvID      string
	MCPURL     string
	HTTP       *http.Client
	MaxRetries int
	RetryBase  time.Duration
	Logf       func(format string, args ...any)

	reqID atomic.Int64

	dirMu    sync.Mutex
	dirCache map[string]dirNode
}

type dirNode struct {
	key       string // 作为子目录/上传的 pdir_key；根目录为空串
	parentKey string // 列出该目录自身时传入的 pdir_key
}

// APIError 表示微云 MCP HTTP 层或业务返回错误。
type APIError struct {
	Op         string
	HTTPStatus int
	Retcode    int
	ErrMsg     string
}

func (e *APIError) Error() string {
	switch {
	case e.HTTPStatus == http.StatusUnauthorized || e.Retcode == 117402 || e.Retcode == 117407:
		return fmt.Sprintf("%s: 微云令牌无效或已过期（%s），请前往 %s 获取新令牌并重新 login", e.Op, e.codeText(), OpenClawURL)
	case e.Retcode == 117401:
		return fmt.Sprintf("%s: 微云 MCP 日配额已用尽（117401）", e.Op)
	case e.HTTPStatus > 0:
		return fmt.Sprintf("%s: HTTP %d %s", e.Op, e.HTTPStatus, e.ErrMsg)
	case e.Retcode != 0 && e.ErrMsg != "":
		return fmt.Sprintf("%s: 微云返回错误 %d（%s）", e.Op, e.Retcode, e.ErrMsg)
	case e.Retcode != 0:
		return fmt.Sprintf("%s: 微云返回错误 %d", e.Op, e.Retcode)
	default:
		return fmt.Sprintf("%s: %s", e.Op, e.ErrMsg)
	}
}

func (e *APIError) codeText() string {
	if e.HTTPStatus == http.StatusUnauthorized {
		return "HTTP 401"
	}
	return fmt.Sprintf("retcode=%d", e.Retcode)
}

func (e *APIError) retryable() bool {
	if e.Retcode == 50000 {
		return true
	}
	return e.HTTPStatus == http.StatusTooManyRequests || e.HTTPStatus >= 500
}

// SplitPath 将微云路径拆为目录段；剥除可选首段 "全部"，拒绝 "." 与 ".."。
func SplitPath(p string) ([]string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if p == "" {
		return nil, fmt.Errorf("云端路径不能为空")
	}
	raw := strings.Split(p, "/")
	var segs []string
	for _, s := range raw {
		if s == "" {
			continue
		}
		if s == "." || s == ".." {
			return nil, fmt.Errorf("云端路径不能包含 %q: %s", s, p)
		}
		if strings.ContainsRune(s, 0) {
			return nil, fmt.Errorf("云端路径包含非法字符")
		}
		segs = append(segs, s)
	}
	if len(segs) > 0 && segs[0] == "全部" {
		segs = segs[1:]
	}
	return segs, nil
}

// CleanPath 规范化微云路径为 "/" 开头的 POSIX 形式。
func CleanPath(p string) (string, error) {
	segs, err := SplitPath(p)
	if err != nil {
		return "", err
	}
	if len(segs) == 0 {
		return "/", nil
	}
	return "/" + strings.Join(segs, "/"), nil
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Client) endpoint() string {
	if strings.TrimSpace(c.MCPURL) != "" {
		return strings.TrimSpace(c.MCPURL)
	}
	return DefaultMCPURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 180 * time.Second}
}

func (c *Client) retries() int {
	if c.MaxRetries < 0 {
		return 0
	}
	return c.MaxRetries
}

func (c *Client) backoff(attempt int) time.Duration {
	base := c.RetryBase
	if base <= 0 {
		base = 2 * time.Second
	}
	wait := base * (1 << (attempt - 1))
	if wait > 30*time.Second {
		wait = 30 * time.Second
	}
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(wait) * jitter)
}

func reqHeader() map[string]string {
	plat := strings.ToUpper(runtime.GOOS)
	if plat == "DARWIN" {
		plat = "MAC"
	}
	return map[string]string{
		"qua":     plat + "_1_ALLPANGO_" + ClientVersion,
		"version": ClientVersion,
	}
}

type rpcPayload struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int64          `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type rpcResponse struct {
	Result *struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error any `json:"error"`
}

func (c *Client) call(op, tool string, args map[string]any, out any) error {
	if strings.TrimSpace(c.MCPToken) == "" {
		return fmt.Errorf("%s: 缺少微云 mcp_token", op)
	}
	copied := make(map[string]any, len(args)+1)
	for k, v := range args {
		copied[k] = v
	}
	if _, ok := copied["req_header"]; !ok {
		copied["req_header"] = reqHeader()
	}

	var lastErr error
	for attempt := 0; attempt <= c.retries(); attempt++ {
		if attempt > 0 {
			wait := c.backoff(attempt)
			c.logf("%s: 第 %d 次重试（%v 后）：%v", op, attempt, wait, lastErr)
			time.Sleep(wait)
		}

		raw, err := c.postRPC(op, tool, copied)
		if err != nil {
			lastErr = err
			var ae *APIError
			if errors.As(err, &ae) && !ae.retryable() {
				return err
			}
			continue
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("%s: 解析微云响应失败: %w", op, err)
			}
		}
		return nil
	}
	return lastErr
}

func (c *Client) postRPC(op, tool string, args map[string]any) ([]byte, error) {
	id := c.reqID.Add(1)
	bodyBytes, err := json.Marshal(rpcPayload{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      tool,
			"arguments": args,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: 编码请求失败: %w", op, err)
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("WyHeader", "mcp_token="+c.MCPToken)
	if c.EnvID != "" {
		req.Header.Set("Cookie", "env_id="+c.EnvID)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	respBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &APIError{Op: op, HTTPStatus: http.StatusUnauthorized}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Op: op, HTTPStatus: resp.StatusCode}
	}
	if readErr != nil {
		return nil, fmt.Errorf("%s: 读响应失败: %w", op, readErr)
	}

	var rpc rpcResponse
	if err := json.Unmarshal(respBytes, &rpc); err != nil {
		return nil, fmt.Errorf("%s: 响应不是合法 JSON: %w", op, err)
	}
	if rpc.Error != nil {
		return nil, &APIError{Op: op, ErrMsg: fmt.Sprintf("MCP 错误: %v", rpc.Error)}
	}
	if rpc.Result == nil {
		return nil, fmt.Errorf("%s: MCP 响应缺少 result", op)
	}

	var payload []byte
	for _, item := range rpc.Result.Content {
		if item.Type == "text" {
			payload = []byte(item.Text)
			break
		}
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("%s: MCP 响应缺少 text 内容", op)
	}

	var probe struct {
		Retcode *int   `json:"retcode"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		if rpc.Result.IsError {
			return nil, &APIError{Op: op, ErrMsg: strings.TrimSpace(string(payload))}
		}
		return nil, fmt.Errorf("%s: 工具结果不是合法 JSON: %w", op, err)
	}
	ret := 0
	if probe.Retcode != nil {
		ret = *probe.Retcode
	}
	if ret != 0 || probe.Error != "" {
		return nil, &APIError{Op: op, Retcode: ret, ErrMsg: probe.Error}
	}
	return payload, nil
}

type listDirItem struct {
	DirKey   string `json:"dir_key"`
	DirName  string `json:"dir_name"`
	Name     string `json:"name"`
	Filename string `json:"filename"`
	DirMtime int64  `json:"dir_mtime"`
}

func (d listDirItem) displayName() string {
	for _, s := range []string{d.DirName, d.Filename, d.Name} {
		if s != "" {
			return s
		}
	}
	return ""
}

type listFileItem struct {
	FileID    string `json:"file_id"`
	Filename  string `json:"filename"`
	Name      string `json:"name"`
	FileSize  *int64 `json:"file_size"`
	Size      *int64 `json:"size"`
	FileMtime int64  `json:"file_mtime"`
	PdirKey   string `json:"pdir_key"`
}

func (f listFileItem) displayName() string {
	if f.Filename != "" {
		return f.Filename
	}
	return f.Name
}

func (f listFileItem) byteSize() int64 {
	if f.FileSize != nil {
		return *f.FileSize
	}
	if f.Size != nil {
		return *f.Size
	}
	return 0
}

type listPageResp struct {
	PdirKey    string         `json:"pdir_key"`
	DirList    []listDirItem  `json:"dir_list"`
	FileList   []listFileItem `json:"file_list"`
	FinishFlag bool           `json:"finish_flag"`
}

func (c *Client) listPage(dirKey, pdirKey string, offset, limit, getType int) (*listPageResp, error) {
	if limit <= 0 || limit > ListLimit {
		limit = ListLimit
	}
	args := map[string]any{
		"limit":    limit,
		"offset":   offset,
		"get_type": getType,
	}
	if dirKey != "" {
		args["dir_key"] = dirKey
	}
	if pdirKey != "" {
		args["pdir_key"] = pdirKey
	}
	var out listPageResp
	if err := c.call("列目录", "weiyun.list", args, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) listAllRaw(dirKey, pdirKey string, getType int) (string, []listDirItem, []listFileItem, error) {
	var dirs []listDirItem
	var files []listFileItem
	var topPdir string
	offset := 0
	for {
		page, err := c.listPage(dirKey, pdirKey, offset, ListLimit, getType)
		if err != nil {
			return "", nil, nil, err
		}
		if page.PdirKey != "" {
			topPdir = page.PdirKey
		}
		dirs = append(dirs, page.DirList...)
		files = append(files, page.FileList...)
		n := len(page.DirList) + len(page.FileList)
		if page.FinishFlag || n == 0 || n < ListLimit {
			break
		}
		offset += ListLimit
		if offset > 1_000_000 {
			return "", nil, nil, fmt.Errorf("微云目录分页超过安全上限")
		}
	}
	return topPdir, dirs, files, nil
}

// VerifyToken 调用最小化列目录接口验证 mcp_token 有效。
func (c *Client) VerifyToken() error {
	_, err := c.listPage("", "", 0, 1, 1)
	return err
}

func (c *Client) cachePut(canonical string, n dirNode) {
	c.dirMu.Lock()
	defer c.dirMu.Unlock()
	if c.dirCache == nil {
		c.dirCache = map[string]dirNode{}
	}
	c.dirCache[canonical] = n
}

// resolveOrCreateDir 串行化目录查找与逐级建目录，避免并发上传时重复创建同一子目录。
func (c *Client) resolveOrCreateDir(segs []string, create bool) (dirNode, error) {
	c.dirMu.Lock()
	defer c.dirMu.Unlock()
	if c.dirCache == nil {
		c.dirCache = map[string]dirNode{}
	}

	target := "/"
	if len(segs) > 0 {
		target = "/" + strings.Join(segs, "/")
	}
	if len(segs) > 0 {
		if n, ok := c.dirCache[target]; ok {
			return n, nil
		}
	}

	// 找到已缓存的最深祖先，减少重复列目录请求。
	curPath := "/"
	cur := dirNode{}
	startIdx := 0
	for i := len(segs); i >= 1; i-- {
		prefix := "/" + strings.Join(segs[:i], "/")
		if n, ok := c.dirCache[prefix]; ok {
			curPath = prefix
			cur = n
			startIdx = i
			break
		}
	}

	if len(segs) == 0 {
		return dirNode{}, nil
	}

	// 若尚未列过当前目录，用 get_type=1 仅拉子目录，避免大目录拉文件清单耗配额。
	var rootPdir string
	if startIdx == 0 {
		if rp, ok := c.dirCache["/_root_pdir"]; ok {
			rootPdir = rp.parentKey
		}
	}

	for i := startIdx; i < len(segs); i++ {
		name := segs[i]
		topPdir, dirs, _, err := c.listAllRaw(cur.key, cur.parentKey, 1)
		if err != nil {
			return dirNode{}, err
		}
		if curPath == "/" && topPdir != "" {
			rootPdir = topPdir
			c.dirCache["/_root_pdir"] = dirNode{parentKey: rootPdir}
		}

		parentForList := cur.key
		if parentForList == "" {
			parentForList = rootPdir
			if parentForList == "" {
				parentForList = topPdir
			}
		}

		var match *listDirItem
		for idx := range dirs {
			if dirs[idx].displayName() == name {
				match = &dirs[idx]
				break
			}
		}

		var childKey string
		if match != nil {
			childKey = match.DirKey
			if childKey == "" {
				return dirNode{}, fmt.Errorf("微云目录缺少 dir_key: %s/%s", curPath, name)
			}
		} else {
			if !create {
				prefix := curPath
				if prefix == "/" {
					prefix = ""
				}
				return dirNode{}, fmt.Errorf("微云目录不存在: %s/%s", prefix, name)
			}
			created, err := c.createDir(name, cur.key)
			if err != nil {
				return dirNode{}, err
			}
			childKey = created
		}

		if curPath == "/" {
			curPath = "/" + name
		} else {
			curPath = curPath + "/" + name
		}
		cur = dirNode{key: childKey, parentKey: parentForList}
		c.dirCache[curPath] = cur
	}
	return cur, nil
}

func (c *Client) createDir(name, pdirKey string) (string, error) {
	args := map[string]any{"dir_name": name}
	if pdirKey != "" {
		args["pdir_key"] = pdirKey
	}
	var out struct {
		DirKey  string `json:"dir_key"`
		DirName string `json:"dir_name"`
	}
	if err := c.call("创建目录", "weiyun.create_dir", args, &out); err != nil {
		return "", err
	}
	if out.DirKey == "" {
		return "", fmt.Errorf("创建目录未返回 dir_key: %s", name)
	}
	if out.DirName != "" && out.DirName != name {
		return "", fmt.Errorf("微云自动重命名新建目录 %q -> %q，停止以免写错路径", name, out.DirName)
	}
	return out.DirKey, nil
}

// ListDir 列出微云指定目录下的直接子目录与文件。
func (c *Client) ListDir(dir string) ([]api.Entry, error) {
	segs, err := SplitPath(dir)
	if err != nil {
		return nil, err
	}
	canon := "/"
	if len(segs) > 0 {
		canon = "/" + strings.Join(segs, "/")
	}
	node, err := c.resolveOrCreateDir(segs, false)
	if err != nil {
		return nil, err
	}
	_, dirs, files, err := c.listAllRaw(node.key, node.parentKey, 0)
	if err != nil {
		return nil, err
	}
	out := make([]api.Entry, 0, len(dirs)+len(files))
	for _, d := range dirs {
		name := d.displayName()
		if name == "" {
			continue
		}
		out = append(out, api.Entry{
			Path:           path.Join(canon, name),
			ServerFilename: name,
			Size:           0,
			IsDir:          1,
			ServerMtime:    normUnixSec(d.DirMtime),
		})
	}
	for _, f := range files {
		name := f.displayName()
		if name == "" {
			continue
		}
		out = append(out, api.Entry{
			Path:           path.Join(canon, name),
			ServerFilename: name,
			Size:           f.byteSize(),
			IsDir:          0,
			ServerMtime:    normUnixSec(f.FileMtime),
			LocalMtime:     0, // 微云接口无客户端本地 mtime，verify 按大小核对
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ServerFilename < out[j].ServerFilename
	})
	return out, nil
}

// ListAll 递归列出微云目录下的全部文件与子目录（满足 sync.Lister）。
func (c *Client) ListAll(dir string) ([]api.Entry, error) {
	segs, err := SplitPath(dir)
	if err != nil {
		return nil, err
	}
	canon := "/"
	if len(segs) > 0 {
		canon = "/" + strings.Join(segs, "/")
	}
	rootNode, err := c.resolveOrCreateDir(segs, false)
	if err != nil {
		return nil, err
	}

	var all []api.Entry
	var walk func(curPath string, node dirNode) error
	walk = func(curPath string, node dirNode) error {
		topPdir, dirs, files, err := c.listAllRaw(node.key, node.parentKey, 0)
		if err != nil {
			return err
		}
		c.logf("列目录 %s（%d 个子目录，%d 个文件）", curPath, len(dirs), len(files))

		parentForChildren := node.key
		if parentForChildren == "" {
			parentForChildren = topPdir
		}

		sort.Slice(files, func(i, j int) bool { return files[i].displayName() < files[j].displayName() })
		for _, f := range files {
			name := f.displayName()
			if name == "" {
				continue
			}
			all = append(all, api.Entry{
				Path:           path.Join(curPath, name),
				ServerFilename: name,
				Size:           f.byteSize(),
				IsDir:          0,
				ServerMtime:    normUnixSec(f.FileMtime),
				LocalMtime:     0,
			})
		}

		sort.Slice(dirs, func(i, j int) bool { return dirs[i].displayName() < dirs[j].displayName() })
		for _, d := range dirs {
			name := d.displayName()
			if name == "" || d.DirKey == "" {
				continue
			}
			childPath := path.Join(curPath, name)
			childNode := dirNode{key: d.DirKey, parentKey: parentForChildren}
			c.cachePut(childPath, childNode)
			all = append(all, api.Entry{
				Path:           childPath,
				ServerFilename: name,
				Size:           0,
				IsDir:          1,
				ServerMtime:    normUnixSec(d.DirMtime),
			})
			if err := walk(childPath, childNode); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(canon, rootNode); err != nil {
		return nil, err
	}
	return all, nil
}

type uploadChannel struct {
	ID     int   `json:"id"`
	Offset int64 `json:"offset"`
	Len    int64 `json:"len"`
}

type uploadResp struct {
	FileID      string          `json:"file_id"`
	Filename    string          `json:"filename"`
	FileExist   bool            `json:"file_exist"`
	UploadState int             `json:"upload_state"`
	UploadKey   string          `json:"upload_key"`
	ChannelList []uploadChannel `json:"channel_list"`
	Ex          string          `json:"ex"`
}

// UploadFile 上传单个文件到微云（支持秒传与 512KB 分片，满足 sync.Uploader）。
func (c *Client) UploadFile(localPath, remotePath string) (*api.UploadResult, error) {
	segs, err := SplitPath(remotePath)
	if err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("upload 需要具体的云端文件路径，不能是根目录")
	}
	parentSegs := segs[:len(segs)-1]
	remoteName := segs[len(segs)-1]
	cleanRemote := "/" + strings.Join(segs, "/")

	params, err := CalcUploadParams(localPath)
	if err != nil {
		return nil, err
	}
	node, err := c.resolveOrCreateDir(parentSegs, true)
	if err != nil {
		return nil, err
	}

	preArgs := map[string]any{
		"filename":       remoteName,
		"file_size":      params.FileSize,
		"file_sha":       params.FileSHA,
		"file_md5":       params.FileMD5,
		"block_sha_list": params.BlockSHAList,
		"check_sha":      params.CheckSHA,
		"check_data":     params.CheckData,
	}
	if node.key != "" {
		preArgs["pdir_key"] = node.key
	}

	f, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	blocksTotal := len(params.BlockSHAList)
	maxRounds := int(params.FileSize/BlockSize)*4 + 32
	if maxRounds < 80 {
		maxRounds = 80
	}

	var rsp uploadResp
	c.logf("预上传 %s（%d 字节，%d 个分块）", cleanRemote, params.FileSize, blocksTotal)
	if err := c.call("预上传", "weiyun.upload", preArgs, &rsp); err != nil {
		return nil, err
	}
	if rsp.FileExist {
		return c.finishResult(cleanRemote, parentSegs, remoteName, params, blocksTotal, 0, true, rsp.Filename), nil
	}

	var uploadedChunks int
	lastID, lastOff := -1, int64(-1)
	samePosCount := 0
	maxSame := c.retries() + 1

	for round := 0; round < maxRounds; round++ {
		ch := firstActiveChannel(rsp.ChannelList)
		if ch == nil {
			if rsp.UploadState == 2 {
				return c.finishResult(cleanRemote, parentSegs, remoteName, params, blocksTotal, uploadedChunks, false, rsp.Filename), nil
			}
			return nil, fmt.Errorf("上传中断 %s: 无可用上传通道（upload_state=%d）", cleanRemote, rsp.UploadState)
		}

		if ch.ID == lastID && ch.Offset == lastOff {
			samePosCount++
			if samePosCount > maxSame {
				return nil, fmt.Errorf("上传停滞 %s: 通道 %d 偏移 %d 无进展", cleanRemote, ch.ID, ch.Offset)
			}
		} else {
			lastID, lastOff = ch.ID, ch.Offset
			samePosCount = 0
		}

		toRead := ch.Len
		remain := params.FileSize - ch.Offset
		if remain <= 0 {
			return nil, fmt.Errorf("微云返回越界偏移 %d（文件大小 %d）", ch.Offset, params.FileSize)
		}
		if toRead > remain {
			toRead = remain
		}
		buf := make([]byte, toRead)
		if _, err := io.ReadFull(io.NewSectionReader(f, ch.Offset, toRead), buf); err != nil {
			return nil, fmt.Errorf("读分片（offset=%d）失败: %w", ch.Offset, err)
		}

		chunkArgs := map[string]any{
			"filename":       remoteName,
			"file_size":      params.FileSize,
			"file_sha":       params.FileSHA,
			"block_sha_list": []string{},
			"check_sha":      params.CheckSHA,
			"upload_key":     rsp.UploadKey,
			"channel_list":   rsp.ChannelList,
			"channel_id":     ch.ID,
			"ex":             rsp.Ex,
			"file_data":      base64.StdEncoding.EncodeToString(buf),
		}
		var up uploadResp
		if err := c.call(fmt.Sprintf("上传分片 %d", uploadedChunks+1), "weiyun.upload", chunkArgs, &up); err != nil {
			return nil, err
		}
		uploadedChunks++
		c.logf("分片 %d 完成（%s，offset=%d, len=%d）", uploadedChunks, cleanRemote, ch.Offset, toRead)

		if up.FileExist {
			return c.finishResult(cleanRemote, parentSegs, remoteName, params, blocksTotal, uploadedChunks, false, up.Filename), nil
		}
		if up.UploadState == 2 {
			return c.finishResult(cleanRemote, parentSegs, remoteName, params, blocksTotal, uploadedChunks, false, up.Filename), nil
		}

		if next := firstActiveChannel(up.ChannelList); next != nil && up.UploadKey != "" && (next.ID != ch.ID || next.Offset != ch.Offset) {
			rsp = up
			continue
		}
		if err := c.call("查询上传通道", "weiyun.upload", preArgs, &rsp); err != nil {
			return nil, err
		}
		if rsp.FileExist {
			return c.finishResult(cleanRemote, parentSegs, remoteName, params, blocksTotal, uploadedChunks, false, rsp.Filename), nil
		}
	}
	return nil, fmt.Errorf("上传 %s 超过最大轮数（%d）", cleanRemote, maxRounds)
}

func (c *Client) finishResult(cleanRemote string, parentSegs []string, remoteName string, p *UploadParams, total, uploaded int, rapid bool, returnedName string) *api.UploadResult {
	actualPath := cleanRemote
	if returnedName != "" && returnedName != remoteName {
		c.logf("提示: 云端返回文件名 %s（上传名 %s）", returnedName, remoteName)
		if len(parentSegs) == 0 {
			actualPath = "/" + returnedName
		} else {
			actualPath = "/" + strings.Join(parentSegs, "/") + "/" + returnedName
		}
	}
	return &api.UploadResult{
		Path:           actualPath,
		Size:           p.FileSize,
		MD5:            p.FileMD5,
		ChunksTotal:    total,
		ChunksUploaded: uploaded,
		Rapid:          rapid,
	}
}

func firstActiveChannel(list []uploadChannel) *uploadChannel {
	for i := range list {
		if list[i].Len > 0 {
			return &list[i]
		}
	}
	return nil
}

func normUnixSec(ts int64) int64 {
	if ts >= 1e11 {
		return ts / 1000
	}
	return ts
}
