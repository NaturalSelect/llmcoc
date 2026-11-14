// NOTE: extract.go 把下载到的文件（zip 压缩包、单个 docx / pdf / txt / md）抽取成纯文本文档。
// 输入来自公网，所以对条目数、单条体积、总体积都设了上限，并且按实际读到的字节数计量而不信任 zip 头部声明的大小。
package onlinemod

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

var (
	// ErrUnsupportedArchive 表示压缩包是 rar 或 7z，本包不解这两种格式。
	ErrUnsupportedArchive = errors.New("不支持 rar / 7z 压缩包")
	// ErrNoDocument 表示没有任何可抽取的文档。
	ErrNoDocument = errors.New("压缩包里没有可提取的文档")

	errArchiveLimit = errors.New("压缩包超出解压限制")
	// errEmptyText 表示文件能解析但没有任何文字（例如只有图片的 docx）。
	errEmptyText = errors.New("文件没有文字内容")
)

// NOTE: 这三个上限是包级变量只是为了让测试能调小；生产代码不要修改。
var (
	maxZipEntries         = 200
	maxEntryBytes   int64 = 30 << 20
	maxArchiveBytes int64 = 200 << 20
)

const (
	// maxTextBytes 是单篇正文的上限，超出按 rune 边界截断。
	maxTextBytes = 2 << 20
	// maxPDFTextBytes 限制 PDF 抽取阶段读入内存的文本量，之后仍会按 maxTextBytes 截断。
	maxPDFTextBytes = 16 << 20
	truncatedNote   = "\n\n[正文超过 2MB，已截断]"

	// 候选正文的判定阈值（skill 规则）：原文件大于 2000 字节且正文超过 3000 字。
	candidateMinFileBytes = 2000
	candidateMinRunes     = 3000

	// maxOCRPerArchive 限制一个压缩包最多送去 OCR 的 PDF 数，免得一个异常压缩包耗光服务商的每日额度。
	maxOCRPerArchive = 5
)

// Document 是抽取出的一篇文档。
type Document struct {
	// Path 对 zip 条目是（解码后的）包内路径，对单文件输入是 filename。
	Path string
	// Kind 是 "docx" | "pdf" | "txt" | "md"。
	Kind string
	// Runes 是正文的 rune 数（不含截断说明）。
	Runes int
	Text  string
	// Candidate 表示它像一篇完整的模组正文：原文件字节数 > 2000 且 Runes > 3000。
	Candidate bool
	// OCR 表示正文来自 OCR 服务：PDF 本地抽取为空或乱码，且 OCR 成功。
	OCR bool
	// Garbled 表示正文疑似乱码且没能修复（没有配置 OCR、OCR 失败或超出次数上限），内容不可信。
	Garbled bool
}

// OCR 是 PDF 本地抽取不可用时的兜底识别服务，返回 Markdown 或纯文本。
type OCR interface {
	ParsePDF(ctx context.Context, name string, data []byte) (string, error)
}

var (
	magicZip = []byte("PK\x03\x04")
	magicPDF = []byte("%PDF")
	magicRar = []byte("Rar!")
	magic7z  = []byte("7z\xbc\xaf\x27\x1c")
)

// Extract 按文件内容的魔数（而不是扩展名）判断类型并抽取正文，结果按 Runes 从多到少排序。不使用 OCR。
func Extract(data []byte, filename string) ([]Document, error) {
	return ExtractWithOCR(context.Background(), data, filename, nil)
}

// ExtractWithOCR 在 Extract 的基础上，对本地抽取为空或乱码的 PDF 调用 ocr 重新识别；ocr 为 nil 时不识别。
// OCR 失败不会让提取失败：保留本地结果，乱码的标记 Garbled，没有文字的照旧丢弃。
func ExtractWithOCR(ctx context.Context, data []byte, filename string, ocr OCR) ([]Document, error) {
	ex := &extractor{ctx: ctx, ocr: ocr}
	switch {
	case bytes.HasPrefix(data, magicZip):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("打开压缩包失败: %w", err)
		}
		if findEntry(zr, docxMainPart) != nil {
			return ex.single(data, "docx", filename)
		}
		return ex.zipDocs(zr)
	case bytes.HasPrefix(data, magicPDF):
		return ex.single(data, "pdf", filename)
	case bytes.HasPrefix(data, magicRar), bytes.HasPrefix(data, magic7z):
		return nil, ErrUnsupportedArchive
	}
	kind := kindByName(filename)
	if kind != "txt" && kind != "md" {
		return nil, ErrNoDocument
	}
	return ex.single(data, kind, filename)
}

// kindByName 只认文本类扩展名，用于没有可识别魔数的单文件。
func kindByName(name string) string {
	switch strings.ToLower(path.Ext(strings.ReplaceAll(name, "\\", "/"))) {
	case ".txt":
		return "txt"
	case ".md", ".markdown":
		return "md"
	}
	return ""
}

// kindByEntry 用于 zip 条目，额外认 docx 和 pdf。
func kindByEntry(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".docx":
		return "docx"
	case ".pdf":
		return "pdf"
	}
	return kindByName(name)
}

type extractor struct {
	ctx context.Context
	ocr OCR
	// total 是已经从压缩包里解出的字节数，包含 docx 内部的 document.xml。
	total int64
	// ocrCalls 是已经送去 OCR 的次数（含失败）。
	ocrCalls int
}

func (ex *extractor) single(data []byte, kind, filename string) ([]Document, error) {
	name := filename
	if name == "" {
		name = "document." + kind
	}
	doc, err := ex.document(data, kind, name)
	if errors.Is(err, errEmptyText) {
		return nil, fmt.Errorf("%w: %s", ErrNoDocument, name)
	}
	if err != nil {
		return nil, fmt.Errorf("提取 %s 失败: %w", name, err)
	}
	return []Document{doc}, nil
}

func (ex *extractor) zipDocs(zr *zip.Reader) ([]Document, error) {
	if len(zr.File) > maxZipEntries {
		return nil, fmt.Errorf("%w: 包含 %d 个条目，上限 %d", errArchiveLimit, len(zr.File), maxZipEntries)
	}
	var docs []Document
	for _, f := range zr.File {
		name := entryName(f)
		if skipEntry(name) {
			continue
		}
		kind := kindByEntry(name)
		if kind == "" {
			continue
		}
		body, err := ex.readFile(f)
		if errors.Is(err, errArchiveLimit) {
			return nil, fmt.Errorf("%w（条目 %s）", err, name)
		}
		if err != nil {
			log.Warn("skip zip entry: read failed", "entry", name, "err", err)
			continue
		}
		doc, err := ex.document(body, kind, name)
		if errors.Is(err, errArchiveLimit) {
			return nil, fmt.Errorf("%w（条目 %s）", err, name)
		}
		if err != nil {
			log.Warn("skip zip entry: extract failed", "entry", name, "kind", kind, "err", err)
			continue
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, ErrNoDocument
	}
	sortDocs(docs)
	return docs, nil
}

func sortDocs(docs []Document) {
	sort.SliceStable(docs, func(i, j int) bool { return docs[i].Runes > docs[j].Runes })
}

// readFile 读出一个 zip 条目。用 LimitReader 按实际字节数限制单条和总量，不信任头部声明的大小。
func (ex *extractor) readFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	allowed := min(maxEntryBytes, maxArchiveBytes-ex.total)
	data, err := io.ReadAll(io.LimitReader(rc, allowed+1))
	if err != nil {
		return nil, err
	}
	switch {
	case int64(len(data)) > maxEntryBytes:
		return nil, fmt.Errorf("%w: 单个文件解压后超过 %d 字节", errArchiveLimit, maxEntryBytes)
	case int64(len(data)) > allowed:
		return nil, fmt.Errorf("%w: 解压总量超过 %d 字节", errArchiveLimit, maxArchiveBytes)
	}
	ex.total += int64(len(data))
	return data, nil
}

// entryName 返回解码后的包内路径。
// NOTE: 国内工具常用 GBK 写文件名且不置 UTF-8 标志。名字本身已是合法 UTF-8 时保持原样，
// 因为 Info-ZIP 等工具会写没有标志的 UTF-8 名字，对它再按 GB18030 解码会变成乱码；
// GBK 字节恰好构成合法 UTF-8 的概率极低。
func entryName(f *zip.File) string {
	name := f.Name
	if !utf8.ValidString(name) {
		if dec, err := simplifiedchinese.GB18030.NewDecoder().String(name); err == nil {
			name = dec
		}
		name = strings.ToValidUTF8(name, "")
	}
	name = strings.ReplaceAll(name, "\\", "/")
	return strings.TrimLeft(name, "/")
}

// skipEntry 跳过目录、__MACOSX、点开头的文件和 Office 的 ~$ 锁文件。
func skipEntry(name string) bool {
	if name == "" || strings.HasSuffix(name, "/") {
		return true
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "__MACOSX" || strings.HasPrefix(seg, ".") || strings.HasPrefix(seg, "~$") {
			return true
		}
	}
	return false
}

func findEntry(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// document 抽取一份已读入内存的文件，raw 是原文件字节。
func (ex *extractor) document(raw []byte, kind, name string) (Document, error) {
	var text string
	var err error
	var ocred, garbled bool
	switch kind {
	case "docx":
		text, err = ex.docxText(raw)
	case "pdf":
		text, err = pdfText(raw)
		if err == nil && needsOCR(text) {
			if better, ok := ex.ocrText(raw, name); ok {
				text, ocred = better, true
			} else {
				garbled = looksGarbled(text)
			}
		}
	case "txt", "md":
		text = decodeText(raw)
	default:
		return Document{}, fmt.Errorf("未知类型 %q", kind)
	}
	if err != nil {
		return Document{}, err
	}
	text, runes := finalizeText(text)
	// NOTE: 放在 finalizeText 之后：先清掉非法 UTF-8，strings.Map 才不会把它们变成替换字符。
	// 部首字符和对应汉字都是 3 字节、1 个 rune，所以截断位置和 runes 都不受影响。
	text = normalizeRadicals(text)
	if strings.TrimSpace(text) == "" {
		return Document{}, errEmptyText
	}
	return Document{
		Path:      name,
		Kind:      kind,
		Runes:     runes,
		Text:      text,
		Candidate: !garbled && len(raw) > candidateMinFileBytes && runes > candidateMinRunes,
		OCR:       ocred,
		Garbled:   garbled,
	}, nil
}

// ocrText 把本地抽不出可用文字的 PDF 交给 OCR；返回的文字仍不可用时视为失败。
func (ex *extractor) ocrText(raw []byte, name string) (string, bool) {
	if ex.ocr == nil || ex.ocrCalls >= maxOCRPerArchive || ex.ctx.Err() != nil {
		return "", false
	}
	ex.ocrCalls++
	text, err := ex.ocr.ParsePDF(ex.ctx, path.Base(name), raw)
	if err != nil {
		log.Warn("pdf ocr failed, keep local text", "entry", name, "err", err)
		return "", false
	}
	if needsOCR(text) {
		log.Warn("pdf ocr returned unusable text, keep local text", "entry", name)
		return "", false
	}
	return text, true
}

// finalizeText 清掉非法 UTF-8 和 NUL，超过上限时按 rune 边界截断并追加说明；返回的 runes 不含说明。
func finalizeText(text string) (string, int) {
	text = strings.ToValidUTF8(text, "")
	text = strings.ReplaceAll(text, "\x00", "")
	if len(text) > maxTextBytes {
		cut := maxTextBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
		return text + truncatedNote, utf8.RuneCountInString(text)
	}
	return text, utf8.RuneCountInString(text)
}

// decodeText 解码 txt / md：去 UTF-8 BOM，认 UTF-16 BOM，其余非法 UTF-8 按 GB18030 处理，并统一换行符。
func decodeText(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xff, 0xfe}), bytes.HasPrefix(b, []byte{0xfe, 0xff}):
		if dec, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder().Bytes(b); err == nil {
			b = dec
		}
	case !utf8.Valid(b):
		if dec, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b); err == nil {
			b = dec
		}
	}
	text := strings.TrimPrefix(string(b), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

const docxMainPart = "word/document.xml"

var (
	// NOTE: <w:tab/> 只认无属性的写法：<w:tabs> 里带属性的 <w:tab w:pos=…/> 是制表位定义，不是制表符。
	// <w:br> 的属性（如 w:type="page"）无关紧要，一律当换行。
	reDocxMark = regexp.MustCompile(`</w:p>|</w:tc>|</w:tr>|<w:br\b[^>]*>|<w:tab\s*/>`)
	reXMLTag   = regexp.MustCompile(`<[^>]+>`)
	reSpaceNL  = regexp.MustCompile(`[ \t]+\n`)
	reManyNL   = regexp.MustCompile(`\n{3,}`)
)

// docxText 照 skill 文档 convert.md 的 docx_text：只读 word/document.xml，把段落、表格、换行还原成文本。
func (ex *extractor) docxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("不是有效的 docx: %w", err)
	}
	f := findEntry(zr, docxMainPart)
	if f == nil {
		return "", fmt.Errorf("docx 缺少 %s", docxMainPart)
	}
	xml, err := ex.readFile(f)
	if err != nil {
		return "", err
	}
	s := reDocxMark.ReplaceAllStringFunc(string(xml), func(m string) string {
		switch {
		case m == "</w:tc>":
			return " | "
		case strings.HasPrefix(m, "<w:tab"):
			return "\t"
		}
		return "\n"
	})
	s = reXMLTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reSpaceNL.ReplaceAllString(s, "\n")
	s = reManyNL.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s), nil
}

// pdfText 用纯 Go 库抽取 PDF 文本。畸形 PDF 会让库 panic，这里统一转成错误。
// NOTE: 不用 Reader.GetPlainText：它按字体资源名（F9、C2_0 这类）跨页缓存解码表，只认第一次见到的那份。
// 很多 PDF 每页的子集字体都叫同一个名字但字形映射不同，整本提取时后面的页会用错解码表，全变成 U+FFFD。
// 逐页独立提取（不传字体缓存）解码是对的。单页失败只丢这一页，整本都失败才报错。
func pdfText(data []byte) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			text, err = "", fmt.Errorf("PDF 解析失败: %v", r)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("PDF 解析失败: %w", err)
	}
	var sb strings.Builder
	var firstErr error
	for i := 1; i <= r.NumPage() && sb.Len() < maxPDFTextBytes; i++ {
		page, perr := pdfPageText(r, i)
		if perr != nil {
			if firstErr == nil {
				firstErr = perr
			}
			log.Warn("skip pdf page: extract failed", "page", i, "err", perr)
			continue
		}
		sb.WriteString(page)
	}
	if sb.Len() == 0 && firstErr != nil {
		return "", fmt.Errorf("PDF 解析失败: %w", firstErr)
	}
	return joinVerticalLines(sb.String()), nil
}

func pdfPageText(r *pdf.Reader, i int) (text string, err error) {
	defer func() {
		if p := recover(); p != nil {
			text, err = "", fmt.Errorf("%v", p)
		}
	}()
	page := r.Page(i)
	if pageNeedsToUnicode(page) {
		return toUnicodePageText(page)
	}
	return page.GetPlainText(nil)
}

// reSentenceEnd 在句末标点后补换行；后一个字符是标点或收引号时不拆，免得把「。」」拆开。
var reSentenceEnd = regexp.MustCompile(`([。！？])([^\n。！？”’」』）)"'])`)

// joinVerticalLines 处理逐字换行的 PDF：超过 60% 的非空行只有 1–2 个字时，
// 去掉行内换行，空行当段落分隔，再在句末标点后断行。普通 PDF 原样返回。
func joinVerticalLines(text string) string {
	lines := strings.Split(text, "\n")
	nonEmpty, short := 0, 0
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		nonEmpty++
		if utf8.RuneCountInString(t) <= 2 {
			short++
		}
	}
	if nonEmpty == 0 || short*10 <= nonEmpty*6 {
		return text
	}
	var sb strings.Builder
	paragraph := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			paragraph = sb.Len() > 0
			continue
		}
		if paragraph {
			sb.WriteString("\n\n")
			paragraph = false
		}
		sb.WriteString(t)
	}
	return reSentenceEnd.ReplaceAllString(sb.String(), "$1\n$2")
}
