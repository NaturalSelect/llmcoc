// NOTE: extract_test.go 覆盖正文抽取。zip / docx / pdf 都在测试里现场构造；
// GBK 字节、docx 期望文本和逐字换行的期望值由独立脚本（python 的 gbk 编码与 convert.md 的 docx_text 逻辑）推出，
// 不是把实现的输出抄进断言。
package onlinemod

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

type zipEnt struct {
	name    string
	body    []byte
	nonUTF8 bool
}

func buildZip(t *testing.T, ents ...zipEnt) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range ents {
		f, err := w.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate, NonUTF8: e.nonUTF8})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildDocx(t *testing.T, bodyXML string) []byte {
	t.Helper()
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		bodyXML + `</w:body></w:document>`
	return buildZip(t,
		zipEnt{name: "[Content_Types].xml", body: []byte(`<Types/>`)},
		zipEnt{name: "word/document.xml", body: []byte(xml)},
	)
}

func para(s string) string { return "<w:p><w:r><w:t>" + s + "</w:t></w:r></w:p>" }

// 汉字文本：n 个 rune，且每个 rune 3 字节。
func cjk(n int) string { return strings.Repeat("文", n) }

func TestExtractDocxTableAndBreaks(t *testing.T) {
	body := `<w:p><w:r><w:t>第一章 &amp; 序</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>甲</w:t><w:br/><w:t>乙</w:t><w:tab/><w:t>丙</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>丁</w:t><w:br w:type="page"/><w:t>戊 &lt;己&gt;</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>姓名</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>力量</w:t></w:r></w:p></w:tc></w:tr>` +
		`<w:tr><w:tc><w:p><w:r><w:t>张三</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>60</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`<w:p><w:pPr><w:tabs><w:tab w:val="left" w:pos="720"/></w:tabs></w:pPr><w:r><w:t>尾 </w:t></w:r></w:p>`
	docs, err := Extract(buildDocx(t, body), "鬼屋.docx")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("文档数 = %d", len(docs))
	}
	// 期望值来自按 convert.md 的 docx_text 逻辑写的 python 脚本（<w:br .../> 按需求用正则处理）。
	want := "第一章 & 序\n甲\n乙\t丙\n丁\n戊 <己>\n姓名\n | 力量\n |\n张三\n | 60\n |\n尾"
	d := docs[0]
	if d.Text != want {
		t.Fatalf("docx 文本\n got  %q\n want %q", d.Text, want)
	}
	if d.Path != "鬼屋.docx" || d.Kind != "docx" || d.Runes != utf8.RuneCountInString(want) {
		t.Fatalf("元数据不对: path=%q kind=%q runes=%d", d.Path, d.Kind, d.Runes)
	}
}

func TestExtractDocxCollapsesBlankLines(t *testing.T) {
	docs, err := Extract(buildDocx(t, para("甲")+"<w:p/><w:p></w:p>"+"<w:p></w:p><w:p></w:p>"+para("乙")), "a.docx")
	if err != nil {
		t.Fatal(err)
	}
	// 4 个空段落加上甲后的换行共 5 个连续换行，压成 2 个。
	if got, want := docs[0].Text, "甲\n\n乙"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractZipWithGBKNames(t *testing.T) {
	// "\xc4\xa3\xd7\xe9" 是「模组」的 GBK 编码，"\xcb\xb5\xc3\xf7" 是「说明」，"\xd7\xd3\xc4\xbf\xc2\xbc" 是「子目录」（python 的 str.encode("gbk") 得出）。
	data := buildZip(t,
		zipEnt{name: "\xc4\xa3\xd7\xe9.txt", body: []byte("模组正文"), nonUTF8: true},
		zipEnt{name: "\xd7\xd3\xc4\xbf\xc2\xbc/\xcb\xb5\xc3\xf7.txt", body: []byte("说明正文内容更长一点"), nonUTF8: true},
	)
	docs, err := Extract(data, "x.zip")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, d := range docs {
		paths = append(paths, d.Path)
	}
	// 按 Runes 从多到少：说明正文内容更长一点 (10) 在前。
	want := []string{"子目录/说明.txt", "模组.txt"}
	if fmt.Sprint(paths) != fmt.Sprint(want) {
		t.Fatalf("paths = %q, want %q", paths, want)
	}
}

// macOS 自带的压缩工具写 UTF-8 文件名但不置 UTF-8 标志：已经是合法 UTF-8 的名字不能再当 GBK 解一遍。
func TestExtractZipKeepsValidUTF8NamesWithoutFlag(t *testing.T) {
	data := buildZip(t, zipEnt{name: "中文说明.txt", body: []byte("正文"), nonUTF8: true})
	docs, err := Extract(data, "x.zip")
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Path != "中文说明.txt" {
		t.Fatalf("path = %q", docs[0].Path)
	}
}

func TestExtractGBKTextAndBOM(t *testing.T) {
	// 「禁止将本模组投喂给AI，违者必究。」的 GBK 字节，由 python 的 str.encode("gbk") 得出。
	gbk := "\xbd\xfb\xd6\xb9\xbd\xab\xb1\xbe\xc4\xa3\xd7\xe9\xcd\xb6\xce\xb9\xb8\xf8\x41\x49\xa3\xac\xce\xa5\xd5\xdf\xb1\xd8\xbe\xbf\xa1\xa3"
	tests := []struct {
		name  string
		data  []byte
		fname string
		want  string
		kind  string
	}{
		{"GBK txt", []byte(gbk), "说明.txt", "禁止将本模组投喂给AI，违者必究。", "txt"},
		{"GBK md", []byte(gbk), "README.MD", "禁止将本模组投喂给AI，违者必究。", "md"},
		{"UTF-8 BOM", []byte("\xef\xbb\xbf你好"), "a.txt", "你好", "txt"},
		{"UTF-16LE BOM", []byte{0xff, 0xfe, 0x60, 0x4f, 0x7d, 0x59}, "a.txt", "你好", "txt"},
		{"CRLF 统一", []byte("甲\r\n乙\r丙"), "a.txt", "甲\n乙\n丙", "txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs, err := Extract(tt.data, tt.fname)
			if err != nil {
				t.Fatal(err)
			}
			if docs[0].Text != tt.want || docs[0].Kind != tt.kind || docs[0].Path != tt.fname {
				t.Fatalf("got %+v", docs[0])
			}
		})
	}
}

func TestExtractMagicNumbers(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		file string
		want error
	}{
		{"rar", append([]byte("Rar!\x1a\x07\x00"), make([]byte, 64)...), "mod.zip", ErrUnsupportedArchive},
		{"7z", append([]byte("7z\xbc\xaf\x27\x1c\x00\x04"), make([]byte, 64)...), "mod.7z", ErrUnsupportedArchive},
		{"未知扩展名", []byte("hello"), "mod.doc", ErrNoDocument},
		{"没有扩展名", []byte("hello"), "", ErrNoDocument},
		{"空 zip", buildZip(t), "mod.zip", ErrNoDocument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Extract(tt.data, tt.file); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestExtractZipSkipsJunkAndNested(t *testing.T) {
	inner := buildZip(t, zipEnt{name: "inner.txt", body: []byte("嵌套")})
	data := buildZip(t,
		zipEnt{name: "__MACOSX/._real.txt", body: []byte("垃圾")},
		zipEnt{name: "__MACOSX/子/x.md", body: []byte("垃圾")},
		zipEnt{name: ".hidden.txt", body: []byte("隐藏")},
		zipEnt{name: "dir/.DS_Store.md", body: []byte("隐藏")},
		zipEnt{name: "~$lock.docx", body: []byte("锁文件")},
		zipEnt{name: "目录/"},
		zipEnt{name: "inner.zip", body: inner},
		zipEnt{name: "readme.exe", body: []byte("MZ")},
		zipEnt{name: "real.md", body: []byte("# 正文")},
	)
	docs, err := Extract(data, "x.zip")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Path != "real.md" || docs[0].Kind != "md" {
		t.Fatalf("docs = %+v", docs)
	}
	onlyNested := buildZip(t, zipEnt{name: "inner.zip", body: inner})
	if _, err := Extract(onlyNested, "x.zip"); !errors.Is(err, ErrNoDocument) {
		t.Fatalf("嵌套 zip 不应递归，err = %v", err)
	}
}

func TestExtractZipWithDocxAndPDFEntries(t *testing.T) {
	docx := buildDocx(t, para("来自 docx 的正文"))
	data := buildZip(t,
		zipEnt{name: "a/模组.docx", body: docx},
		zipEnt{name: "b.pdf", body: minimalPDF("Hello Module PDF")},
		zipEnt{name: "c.txt", body: []byte("纯文本")},
	)
	docs, err := Extract(data, "x.zip")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Document{}
	for _, d := range docs {
		byPath[d.Path] = d
	}
	if len(docs) != 3 {
		t.Fatalf("docs = %+v", docs)
	}
	if d := byPath["a/模组.docx"]; d.Kind != "docx" || d.Text != "来自 docx 的正文" {
		t.Errorf("docx = %+v", d)
	}
	if d := byPath["b.pdf"]; d.Kind != "pdf" || !strings.Contains(d.Text, "Hello Module PDF") {
		t.Errorf("pdf = %+v", d)
	}
}

func TestExtractZipSkipsBrokenEntries(t *testing.T) {
	data := buildZip(t,
		zipEnt{name: "bad.docx", body: []byte("not a zip at all")},
		zipEnt{name: "bad.pdf", body: []byte("%PDF-1.4\n\x00\x01garbage")},
		zipEnt{name: "empty.txt", body: []byte(" \n\t ")},
		zipEnt{name: "ok.txt", body: []byte("好的")},
	)
	docs, err := Extract(data, "x.zip")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Path != "ok.txt" {
		t.Fatalf("docs = %+v", docs)
	}
	allBad := buildZip(t,
		zipEnt{name: "bad.docx", body: []byte("not a zip")},
		zipEnt{name: "bad.pdf", body: []byte("%PDF-1.4\nxx")},
	)
	if _, err := Extract(allBad, "x.zip"); !errors.Is(err, ErrNoDocument) {
		t.Fatalf("全部失败应返回 ErrNoDocument，err = %v", err)
	}
}

func TestExtractZipEntryCountLimit(t *testing.T) {
	mk := func(n int) []byte {
		ents := make([]zipEnt, n)
		for i := range ents {
			ents[i] = zipEnt{name: fmt.Sprintf("f%03d.txt", i), body: []byte("x")}
		}
		return buildZip(t, ents...)
	}
	if docs, err := Extract(mk(200), "x.zip"); err != nil || len(docs) != 200 {
		t.Fatalf("200 个条目应通过: n=%d err=%v", len(docs), err)
	}
	if _, err := Extract(mk(201), "x.zip"); !errors.Is(err, errArchiveLimit) {
		t.Fatalf("201 个条目应超限，err = %v", err)
	}
}

func TestExtractZipEntrySizeLimit(t *testing.T) {
	// 31MB 的零字节压缩后只有几十 KB，但解压后超过 30MB 上限。
	big := bytes.Repeat([]byte("a"), 31<<20)
	data := buildZip(t, zipEnt{name: "big.txt", body: big}, zipEnt{name: "small.txt", body: []byte("x")})
	if _, err := Extract(data, "x.zip"); !errors.Is(err, errArchiveLimit) {
		t.Fatalf("err = %v，期望 errArchiveLimit", err)
	}
	exact := bytes.Repeat([]byte("a"), 30<<20)
	if docs, err := Extract(buildZip(t, zipEnt{name: "exact.txt", body: exact}), "x.zip"); err != nil || len(docs) != 1 {
		t.Fatalf("恰好 30MB 应通过: err=%v", err)
	}
}

func TestExtractZipTotalSizeLimit(t *testing.T) {
	old := maxArchiveBytes
	maxArchiveBytes = 1500
	t.Cleanup(func() { maxArchiveBytes = old })

	body := bytes.Repeat([]byte("a"), 600)
	ok := buildZip(t, zipEnt{name: "1.txt", body: body}, zipEnt{name: "2.txt", body: body})
	if _, err := Extract(ok, "x.zip"); err != nil {
		t.Fatalf("1200 字节在 1500 上限内，err = %v", err)
	}
	over := buildZip(t, zipEnt{name: "1.txt", body: body}, zipEnt{name: "2.txt", body: body}, zipEnt{name: "3.txt", body: body})
	if _, err := Extract(over, "x.zip"); !errors.Is(err, errArchiveLimit) {
		t.Fatalf("1800 字节应超过总量上限，err = %v", err)
	}
}

// 单个 docx 的 document.xml 也要受解压上限约束（docx 炸弹）。
func TestExtractDocxXMLSizeLimit(t *testing.T) {
	old := maxEntryBytes
	maxEntryBytes = 1000
	t.Cleanup(func() { maxEntryBytes = old })
	data := buildDocx(t, para(strings.Repeat("a", 5000)))
	if _, err := Extract(data, "x.docx"); !errors.Is(err, errArchiveLimit) {
		t.Fatalf("err = %v，期望 errArchiveLimit", err)
	}
}

func TestExtractSortedByRunesDescending(t *testing.T) {
	data := buildZip(t,
		zipEnt{name: "short.txt", body: []byte(cjk(10))},
		zipEnt{name: "long.txt", body: []byte(cjk(500))},
		zipEnt{name: "mid.txt", body: []byte(cjk(100))},
	)
	docs, err := Extract(data, "x.zip")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{docs[0].Path, docs[1].Path, docs[2].Path}
	if fmt.Sprint(got) != "[long.txt mid.txt short.txt]" {
		t.Fatalf("顺序 = %v", got)
	}
	if docs[0].Runes != 500 || docs[2].Runes != 10 {
		t.Fatalf("Runes = %d / %d", docs[0].Runes, docs[2].Runes)
	}
}

func TestCandidateThresholds(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"3000 字不够", cjk(3000), false},
		{"3001 字入选", cjk(3001), true},
		{"2999 个 ASCII", strings.Repeat("a", 2999), false},
		{"3001 个 ASCII", strings.Repeat("a", 3001), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs, err := Extract([]byte(tt.text), "a.txt")
			if err != nil {
				t.Fatal(err)
			}
			if docs[0].Candidate != tt.want {
				t.Fatalf("Runes=%d Candidate=%v, want %v", docs[0].Runes, docs[0].Candidate, tt.want)
			}
		})
	}
	// 原文件字节数也要超过 2000：重复文字压缩得很小的 docx 即使正文超过 3000 字也不算候选（skill 规则）。
	small := buildDocx(t, para(strings.Repeat("a", 3500)))
	if len(small) > 2000 {
		t.Fatalf("前提不成立：docx 有 %d 字节", len(small))
	}
	docs, err := Extract(small, "a.docx")
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Runes != 3500 || docs[0].Candidate {
		t.Fatalf("Runes=%d Candidate=%v，原文件不足 2000 字节不应入选", docs[0].Runes, docs[0].Candidate)
	}
}

func TestExtractTruncatesHugeText(t *testing.T) {
	// 700000 个汉字 = 2100000 字节，超过 2MB（2097152）。
	docs, err := Extract([]byte(cjk(700000)), "huge.txt")
	if err != nil {
		t.Fatal(err)
	}
	d := docs[0]
	if !strings.HasSuffix(d.Text, truncatedNote) || !utf8.ValidString(d.Text) {
		t.Fatalf("应以截断说明结尾且仍是合法 UTF-8: tail=%q", d.Text[max(0, len(d.Text)-60):])
	}
	// 2097152 / 3 向下取整 = 699050 个完整汉字；说明文字不计入 Runes。
	if d.Runes != 699050 {
		t.Fatalf("Runes = %d, want 699050", d.Runes)
	}
	if len(d.Text) != 699050*3+len(truncatedNote) {
		t.Fatalf("len(Text) = %d", len(d.Text))
	}
}

func TestExtractStripsInvalidUTF8(t *testing.T) {
	// 以 PK 之外开头的合法 UTF-8 文本中混入 NUL，应被清掉。
	docs, err := Extract([]byte("甲\x00乙"), "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Text != "甲乙" {
		t.Fatalf("text = %q", docs[0].Text)
	}
}

// 畸形 PDF 会让底层库 panic，必须转成错误，而且不能卡死。
func TestExtractMalformedPDFDoesNotPanic(t *testing.T) {
	samples := map[string][]byte{
		"只有头":          []byte("%PDF-1.4"),
		"垃圾":           append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{0x00, 0xff, 0x7f, 'x'}, 200)...),
		"startxref 越界": []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\nxref\n0 1\n0000000000 65535 f \ntrailer\n<< /Root 1 0 R >>\nstartxref\n99999999\n%%EOF"),
		"页树损坏":         []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [9 0 R] /Count 1 >>\nendobj\ntrailer\n<< /Root 1 0 R >>\nstartxref\n0\n%%EOF"),
		"截断的有效 PDF":    minimalPDF("Hello")[:120],
	}
	for name, data := range samples {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Extract panic: %v", r)
				}
			}()
			if docs, err := Extract(data, "bad.pdf"); err == nil {
				t.Fatalf("畸形 PDF 应返回错误，得到 %+v", docs)
			}
		})
	}
	t.Run("pdfText 直接调用", func(t *testing.T) {
		if _, err := pdfText([]byte("%PDF-1.4\nxx")); err == nil {
			t.Fatal("应返回错误")
		}
	})
}

func TestExtractValidPDF(t *testing.T) {
	docs, err := Extract(minimalPDF("Hello Module PDF"), "mod.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Kind != "pdf" || docs[0].Path != "mod.pdf" || !strings.Contains(docs[0].Text, "Hello Module PDF") {
		t.Fatalf("doc = %+v", docs[0])
	}
}

// minimalPDF 手工拼一个单页 PDF（Helvetica，一行 ASCII 文字），xref 偏移按实际长度计算。
func minimalPDF(text string) []byte {
	var b bytes.Buffer
	var offsets [6]int
	b.WriteString("%PDF-1.4\n")
	obj := func(n int, body string) {
		offsets[n] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	obj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	obj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	xref := b.Len()
	b.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return b.Bytes()
}

func TestJoinVerticalLines(t *testing.T) {
	vertical := func(s string) string { return strings.Join(strings.Split(s, ""), "\n") }

	t.Run("逐字换行合并并恢复句子与段落", func(t *testing.T) {
		in := vertical("这是第一句话。第二句话在这里。") + "\n\n" + vertical("新的段落开始了。")
		// 手工推出：去掉行内换行得到「这是第一句话。第二句话在这里。」，空行变段落分隔，句号后断行。
		want := "这是第一句话。\n第二句话在这里。\n\n新的段落开始了。"
		if got := joinVerticalLines(in); got != want {
			t.Fatalf("\n got  %q\n want %q", got, want)
		}
	})
	t.Run("两字一行也算", func(t *testing.T) {
		in := "禁止\n投喂\nAI\n本\n模组\n。"
		if got, want := joinVerticalLines(in), "禁止投喂AI本模组。"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("正常排版原样返回", func(t *testing.T) {
		in := "这是一段正常的文字，每行都很长。\n第二行同样足够长，不应该被合并。\n\n第三段。"
		if got := joinVerticalLines(in); got != in {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("恰好 60% 不合并，超过才合并", func(t *testing.T) {
		long := "这一行有很多个字"
		mk := func(short int) string {
			lines := make([]string, 0, 10)
			for i := 0; i < 10; i++ {
				if i < short {
					lines = append(lines, "字")
				} else {
					lines = append(lines, long)
				}
			}
			return strings.Join(lines, "\n")
		}
		if got := joinVerticalLines(mk(6)); got != mk(6) {
			t.Fatalf("6/10 恰好 60%% 不应合并: %q", got)
		}
		if got := joinVerticalLines(mk(7)); strings.Contains(got, "\n") {
			t.Fatalf("7/10 应合并成一行: %q", got)
		}
	})
	t.Run("空文本", func(t *testing.T) {
		if got := joinVerticalLines(""); got != "" {
			t.Fatalf("got %q", got)
		}
	})
}

// 逐字换行的 PDF 合并后，版权声明要能被扫到（两个功能串起来的场景）。
func TestVerticalPDFTextFeedsLicenseScan(t *testing.T) {
	joined := joinVerticalLines(strings.Join(strings.Split("本模组禁止投喂AI。祝游戏愉快。", ""), "\n"))
	if hits := ScanLicense(joined); len(hits) != 1 || !strings.Contains(hits[0], "禁止投喂AI") {
		t.Fatalf("hits = %q", hits)
	}
}
