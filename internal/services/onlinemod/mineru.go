// NOTE: mineru.go 是 MinerU「精准解析 API」的客户端，只用来给「本地提取出乱码的 PDF」做 OCR 后备。
// 流程：申请上传链接 → PUT 上传 → 轮询批量结果 → 下载结果 zip → 取出 full.md。
// 出站姿态与 client.go 一致：只放行 https 和固定主机白名单，重定向每一跳重验，响应体有大小上限。
// 上传链接和 full_zip_url 来自服务端响应，属于不可信输入，使用前必须过白名单。
// 申请时强制 is_ocr=true：这类 PDF 的文字层本身是坏的，不强制 OCR 的话 MinerU 会读同一个坏文字层。
package onlinemod

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	minerUBaseURL      = "https://mineru.net"
	minerUApplyPath    = "/api/v4/file-urls/batch"
	minerUResultPath   = "/api/v4/extract-results/batch/"
	minerUModelVersion = "vlm"
	minerULanguage     = "ch"
	minerUResultFile   = "full.md"

	defaultMinerUPollInterval = 5 * time.Second
	defaultMinerUPollTimeout  = 8 * time.Minute

	// NOTE: 各阶段的单次请求超时；轮询阶段的总时长另由 pollTimeout 限制。
	minerUAPITimeout      = 30 * time.Second
	minerUUploadTimeout   = 5 * time.Minute
	minerUDownloadTimeout = 3 * time.Minute

	// NOTE: maxMinerUErrText 是写进错误信息的服务端文本长度上限（字节）。
	maxMinerUErrText = 300
)

// NOTE: 这几个上限是包级变量只是为了让测试能调小；生产代码不要修改。
var (
	maxMinerUUploadBytes         = 50 << 20
	maxMinerUJSONBytes     int64 = 1 << 20
	maxMinerUZipBytes      int64 = 64 << 20
	maxMinerUMarkdownBytes int64 = 16 << 20
)

var (
	// NOTE: ErrMinerUNotConfigured 表示没有配置 MinerU token。
	ErrMinerUNotConfigured = errors.New("未配置 MinerU token")
	// NOTE: ErrMinerUTimeout 表示在 pollTimeout 内 MinerU 没有解析完成。
	ErrMinerUTimeout = errors.New("等待 MinerU 解析超时")
)

// NOTE: MinerU 的白名单是独立的一份，不与 allowedSuffixes 共用，且只做精确匹配（不放行子域名）。
// 上传预签名链接只放行上海地域的 OSS 桶域名，其它地域报 ErrURLNotAllowed，放宽前先确认确实属于上传链路。
var minerUAllowedHosts = map[string]bool{
	"mineru.net":                          true,
	"cdn-mineru.openxlab.org.cn":          true,
	"mineru.oss-cn-shanghai.aliyuncs.com": true,
}

func minerUAllowHost(host string) bool {
	return minerUAllowedHosts[strings.ToLower(strings.TrimSuffix(host, "."))]
}

// NOTE: MinerU 可被多个 goroutine 共用；token 只用于发往 baseURL 的 API 请求，
// 不会发给上传链接和结果 CDN，也不会写进日志或错误信息。
type MinerU struct {
	token string
	// NOTE: baseURL 是 API 根地址，默认 https://mineru.net；测试里指向 httptest 服务。
	baseURL string
	hc      *http.Client
	// NOTE: allowHost 判定主机名（不含端口）是否允许访问，默认按白名单；测试里替换成放行 httptest 主机的版本。
	allowHost    func(host string) bool
	pollInterval time.Duration
	pollTimeout  time.Duration
}

// NOTE: NewMinerU 的 token 为空也能构造，此时 Configured 返回 false。不设 Client.Timeout，由 ctx 和各阶段超时控制。
func NewMinerU(token string) *MinerU {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
	}
	return &MinerU{
		token:        strings.TrimSpace(token),
		baseURL:      minerUBaseURL,
		hc:           &http.Client{Transport: tr},
		allowHost:    minerUAllowHost,
		pollInterval: defaultMinerUPollInterval,
		pollTimeout:  defaultMinerUPollTimeout,
	}
}

// NOTE: Configured 报告是否有可用的 token。
func (m *MinerU) Configured() bool {
	return m != nil && m.token != ""
}

// NOTE: ParsePDF 上传 PDF 并等待解析完成，返回 full.md 的文本。页数和体积上限由服务端判定，错误原样带回。
func (m *MinerU) ParsePDF(ctx context.Context, name string, data []byte) (string, error) {
	if !m.Configured() {
		return "", ErrMinerUNotConfigured
	}
	if len(data) == 0 {
		return "", errors.New("PDF 内容为空")
	}
	if len(data) > maxMinerUUploadBytes {
		return "", fmt.Errorf("PDF 大小 %d 字节，超过 MinerU 上传上限 %d 字节", len(data), maxMinerUUploadBytes)
	}
	dataID, err := newMinerUDataID()
	if err != nil {
		return "", fmt.Errorf("生成 data_id 失败: %w", err)
	}
	batchID, uploadURL, err := m.applyUpload(ctx, minerUFileName(name), dataID)
	if err != nil {
		return "", fmt.Errorf("申请 MinerU 上传链接失败: %w", err)
	}
	if err := m.upload(ctx, uploadURL, data); err != nil {
		return "", fmt.Errorf("上传 PDF 到 MinerU 失败: %w", err)
	}
	log.Info("mineru: PDF 已上传，等待解析", "batch_id", batchID, "bytes", len(data))
	zipURL, err := m.waitResult(ctx, batchID)
	if err != nil {
		return "", err
	}
	zipData, err := m.download(ctx, zipURL)
	if err != nil {
		return "", fmt.Errorf("下载 MinerU 解析结果失败: %w", err)
	}
	text, err := extractMinerUMarkdown(zipData)
	if err != nil {
		return "", err
	}
	log.Info("mineru: 解析完成", "batch_id", batchID, "zip_bytes", len(zipData), "md_bytes", len(text))
	return text, nil
}

type minerUFileRequest struct {
	Name   string `json:"name"`
	DataID string `json:"data_id"`
	IsOCR  bool   `json:"is_ocr"`
}

type minerUApplyRequest struct {
	Files         []minerUFileRequest `json:"files"`
	ModelVersion  string              `json:"model_version"`
	Language      string              `json:"language"`
	EnableFormula bool                `json:"enable_formula"`
	EnableTable   bool                `json:"enable_table"`
}

func (m *MinerU) applyUpload(ctx context.Context, fileName, dataID string) (batchID, uploadURL string, err error) {
	req := minerUApplyRequest{
		Files:        []minerUFileRequest{{Name: fileName, DataID: dataID, IsOCR: true}},
		ModelVersion: minerUModelVersion,
		Language:     minerULanguage,
		EnableTable:  true,
	}
	var out struct {
		BatchID  string   `json:"batch_id"`
		FileURLs []string `json:"file_urls"`
	}
	if err := m.callAPI(ctx, http.MethodPost, minerUApplyPath, req, &out); err != nil {
		return "", "", err
	}
	if out.BatchID == "" || len(out.FileURLs) == 0 {
		return "", "", errors.New("MinerU 响应缺少 batch_id 或 file_urls")
	}
	if err := m.checkUpstream("上传地址", out.FileURLs[0]); err != nil {
		return "", "", err
	}
	return out.BatchID, out.FileURLs[0], nil
}

func (m *MinerU) upload(ctx context.Context, uploadURL string, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, minerUUploadTimeout)
	defer cancel()
	// NOTE: 文档要求上传时不设 Content-Type；预签名链接自带签名，也不能带 Authorization，否则 token 会发给 OSS。
	_, status, err := m.do(ctx, http.MethodPut, uploadURL, bytes.NewReader(data), nil, maxMinerUJSONBytes)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return fmt.Errorf("上传返回 HTTP %d", status)
	}
	return nil
}

type minerUExtractItem struct {
	State           string `json:"state"`
	ErrMsg          string `json:"err_msg"`
	FullZipURL      string `json:"full_zip_url"`
	ExtractProgress *struct {
		ExtractedPages int `json:"extracted_pages"`
		TotalPages     int `json:"total_pages"`
	} `json:"extract_progress"`
}

// NOTE: waitResult 轮询到解析完成并返回 full_zip_url。立即先查一次，之后每隔 pollInterval 查一次。
// TODO: 轮询遇到瞬时网络错误或 5xx 时直接失败，没有重试；真实使用中若常因抖动中断再加。
func (m *MinerU) waitResult(ctx context.Context, batchID string) (string, error) {
	timeout := m.timeout()
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lastState := ""
	for {
		item, err := m.pollOnce(pollCtx, batchID)
		if err != nil {
			if ctx.Err() != nil || pollCtx.Err() != nil {
				return "", m.waitInterrupted(ctx, timeout, lastState)
			}
			return "", fmt.Errorf("查询 MinerU 解析结果失败: %w", err)
		}
		if item != nil {
			lastState = item.State
			switch item.State {
			case "waiting-file", "pending", "running", "converting":
				pages, total := 0, 0
				if p := item.ExtractProgress; p != nil {
					pages, total = p.ExtractedPages, p.TotalPages
				}
				log.Debug("mineru: 解析进行中", "batch_id", batchID, "state", item.State, "pages", pages, "total_pages", total)
			case "done":
				if item.FullZipURL == "" {
					return "", errors.New("MinerU 解析完成但没有返回 full_zip_url")
				}
				if err := m.checkUpstream("结果地址", item.FullZipURL); err != nil {
					return "", err
				}
				return item.FullZipURL, nil
			case "failed":
				msg := m.scrub(item.ErrMsg)
				if msg == "" {
					msg = "（服务端没有给出原因）"
				}
				return "", fmt.Errorf("MinerU 解析失败: %s", msg)
			default:
				return "", fmt.Errorf("MinerU 返回未知任务状态 %q", m.scrub(item.State))
			}
		}
		timer := time.NewTimer(m.interval())
		select {
		case <-pollCtx.Done():
			timer.Stop()
			return "", m.waitInterrupted(ctx, timeout, lastState)
		case <-timer.C:
		}
	}
}

// NOTE: waitInterrupted 区分「调用方取消」和「等待超过 pollTimeout」。
func (m *MinerU) waitInterrupted(ctx context.Context, timeout time.Duration, lastState string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("等待 MinerU 解析已中断: %w", err)
	}
	return fmt.Errorf("%w（已等待 %s，最后状态 %q）", ErrMinerUTimeout, timeout, lastState)
}

// NOTE: pollOnce 查询一次批量结果；批次里还没有任务条目时返回 nil，调用方继续等。
func (m *MinerU) pollOnce(ctx context.Context, batchID string) (*minerUExtractItem, error) {
	var out struct {
		ExtractResult []minerUExtractItem `json:"extract_result"`
	}
	if err := m.callAPI(ctx, http.MethodGet, minerUResultPath+url.PathEscape(batchID), nil, &out); err != nil {
		return nil, err
	}
	if len(out.ExtractResult) == 0 {
		return nil, nil
	}
	// NOTE: 每个批次只提交了一个文件，取第一项。
	return &out.ExtractResult[0], nil
}

func (m *MinerU) download(ctx context.Context, zipURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, minerUDownloadTimeout)
	defer cancel()
	// NOTE: 结果 CDN 不需要也不应收到 token。
	data, status, err := m.do(ctx, http.MethodGet, zipURL, nil, nil, maxMinerUZipBytes)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("下载返回 HTTP %d", status)
	}
	return data, nil
}

// NOTE: extractMinerUMarkdown 从结果 zip 里取出 full.md。它可能在子目录下，按文件名 base 精确匹配，多个时取最浅的。
func extractMinerUMarkdown(zipData []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("MinerU 结果不是合法的 zip: %w", err)
	}
	if len(zr.File) > maxZipEntries {
		return "", fmt.Errorf("%w: MinerU 结果包含 %d 个条目，上限 %d", errArchiveLimit, len(zr.File), maxZipEntries)
	}
	var found *zip.File
	foundDepth := 0
	for _, f := range zr.File {
		name := entryName(f)
		if skipEntry(name) || path.Base(name) != minerUResultFile {
			continue
		}
		if depth := strings.Count(name, "/"); found == nil || depth < foundDepth {
			found, foundDepth = f, depth
		}
	}
	if found == nil {
		return "", fmt.Errorf("MinerU 结果压缩包里没有 %s", minerUResultFile)
	}
	rc, err := found.Open()
	if err != nil {
		return "", fmt.Errorf("打开 %s 失败: %w", minerUResultFile, err)
	}
	defer rc.Close()
	// NOTE: 不信任 zip 头里声明的大小，按实际解压字节数限制。
	data, err := io.ReadAll(io.LimitReader(rc, maxMinerUMarkdownBytes+1))
	if err != nil {
		return "", fmt.Errorf("读取 %s 失败: %w", minerUResultFile, err)
	}
	if int64(len(data)) > maxMinerUMarkdownBytes {
		return "", fmt.Errorf("%w: %s 解压后超过 %d 字节", ErrTooLarge, minerUResultFile, maxMinerUMarkdownBytes)
	}
	text := strings.TrimPrefix(strings.ToValidUTF8(string(data), ""), "\ufeff")
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("MinerU 的 %s 没有内容", minerUResultFile)
	}
	return text, nil
}

// NOTE: minerUCode 兼容 MinerU 的两种错误码：数字（-60018）和字符串（A0202）。
type minerUCode string

func (c *minerUCode) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*c = minerUCode(s)
		return nil
	}
	*c = minerUCode(strings.TrimSpace(string(b)))
	return nil
}

type minerUEnvelope struct {
	Code minerUCode      `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// NOTE: callAPI 调用 baseURL 下的 JSON 接口：带 token，code 非 0 视为失败，成功时把 data 解到 out。
func (m *MinerU) callAPI(ctx context.Context, method, apiPath string, reqBody, out any) error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+m.token)
	header.Set("Accept", "application/json")
	var body io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("编码请求失败: %w", err)
		}
		body = bytes.NewReader(b)
		header.Set("Content-Type", "application/json")
	}
	ctx, cancel := context.WithTimeout(ctx, minerUAPITimeout)
	defer cancel()
	data, status, err := m.do(ctx, method, strings.TrimRight(m.baseURL, "/")+apiPath, body, header, maxMinerUJSONBytes)
	if err != nil {
		return err
	}
	var env minerUEnvelope
	jsonErr := json.Unmarshal(data, &env)
	if status < 200 || status > 299 {
		if jsonErr == nil && (env.Code != "" || env.Msg != "") {
			return fmt.Errorf("MinerU 返回 HTTP %d（code=%s msg=%s）", status, m.scrub(string(env.Code)), m.scrub(env.Msg))
		}
		return fmt.Errorf("MinerU 返回 HTTP %d: %s", status, m.scrub(string(data)))
	}
	if jsonErr != nil {
		return fmt.Errorf("MinerU 响应不是合法 JSON: %w", jsonErr)
	}
	if env.Code != "0" {
		return fmt.Errorf("MinerU 返回错误 code=%s msg=%s", m.scrub(string(env.Code)), m.scrub(env.Msg))
	}
	if out == nil {
		return nil
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return errors.New("MinerU 响应缺少 data")
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("解析 MinerU 响应 data 失败: %w", err)
	}
	return nil
}

// NOTE: do 经受限的 Client 发出请求：https、白名单、重定向每跳重验、响应体限长。不判断状态码。
func (m *MinerU) do(ctx context.Context, method, rawURL string, body io.Reader, header http.Header, maxBytes int64) ([]byte, int, error) {
	data, _, status, err := m.fetcher().fetch(ctx, method, rawURL, body, header, maxBytes)
	if err != nil {
		return nil, status, redactURLError(err)
	}
	return data, status, nil
}

func (m *MinerU) fetcher() *Client {
	allow := m.allowHost
	if allow == nil {
		// NOTE: 不能让 Client 回落到它自己的 cnmods 白名单。
		allow = minerUAllowHost
	}
	return &Client{HTTP: m.hc, AllowHost: allow}
}

// NOTE: checkUpstream 在发请求前校验服务端响应里给出的地址；原始地址（可能带签名）不进错误信息。
func (m *MinerU) checkUpstream(what, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("MinerU 返回的%s无法解析", what)
	}
	if err := m.fetcher().checkURL(u); err != nil {
		return fmt.Errorf("MinerU 返回的%s被拒绝: %w", what, err)
	}
	return nil
}

// NOTE: redactURLError 去掉 *url.Error 里的完整地址，只留主机：预签名链接的查询串带签名，不能进日志。
func redactURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	host := "?"
	if u, perr := url.Parse(ue.URL); perr == nil && u.Host != "" {
		host = u.Host
	}
	return fmt.Errorf("%s %s: %w", ue.Op, host, ue.Err)
}

// NOTE: scrub 处理写进错误信息的服务端文本：抹掉 token、限长。
func (m *MinerU) scrub(s string) string {
	if m.token != "" {
		s = strings.ReplaceAll(s, m.token, "***")
	}
	return strings.TrimSpace(head([]byte(s), maxMinerUErrText))
}

func (m *MinerU) interval() time.Duration {
	if m.pollInterval > 0 {
		return m.pollInterval
	}
	return defaultMinerUPollInterval
}

func (m *MinerU) timeout() time.Duration {
	if m.pollTimeout > 0 {
		return m.pollTimeout
	}
	return defaultMinerUPollTimeout
}

// NOTE: minerUFileName 去掉目录部分并保证以 .pdf 结尾（文档强烈建议带正确后缀）。
func minerUFileName(name string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if name == "." || name == "/" {
		name = "document"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}
	return name
}

// NOTE: newMinerUDataID 生成只含字母数字和短横线的随机 data_id（远小于 128 字符），不从文件名派生。
func newMinerUDataID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "llmcoc-" + hex.EncodeToString(b[:]), nil
}
