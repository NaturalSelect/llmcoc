// NOTE: pdf_tounicode_test.go 的期望字由 Unicode 码点推出，不是抄实现的输出：
// U+6C49=汉，U+5B57=字，U+5B58=存，U+4E00=一，U+4E8C=二，U+20BB6=𠮶（UTF-16 代理对 D842 DFB6）。
package onlinemod

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func mustParseCMap(t *testing.T, cmap string) *toUnicodeMap {
	t.Helper()
	m := parseToUnicode(strings.NewReader(cmap))
	if m == nil {
		t.Fatalf("parseToUnicode 返回 nil: %q", cmap)
	}
	return m
}

const cmapHead = "/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n"
const cmapTail = "endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n"

func TestToUnicodeBFRangeAndBFChar(t *testing.T) {
	m := mustParseCMap(t, cmapHead+
		"1 begincodespacerange\n<00> <FF>\nendcodespacerange\n"+
		"2 beginbfrange\n<21> <21> <6C49>\n<22> <23> <5B57>\nendbfrange\n"+
		"1 beginbfchar\n<30> <4E00>\nendbfchar\n"+cmapTail)
	// 0x22 是区间起点 5B57，0x23 是起点加 1 即 5B58。
	if got, want := m.Decode("\x21\x22\x23\x30"), "汉字存一"; got != want {
		t.Fatalf("Decode = %q, want %q", got, want)
	}
}

func TestToUnicodeBFRangeArrayForm(t *testing.T) {
	m := mustParseCMap(t, cmapHead+
		"1 begincodespacerange\n<00> <FF>\nendcodespacerange\n"+
		"1 beginbfrange\n<40> <41> [<4E00> <4E8C>]\nendbfrange\n"+cmapTail)
	if got, want := m.Decode("\x40\x41"), "一二"; got != want {
		t.Fatalf("Decode = %q, want %q", got, want)
	}
}

func TestToUnicodeTwoByteCodespace(t *testing.T) {
	m := mustParseCMap(t, cmapHead+
		"1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n"+
		"1 beginbfchar\n<0001> <6C49>\nendbfchar\n"+cmapTail)
	if got, want := m.Decode("\x00\x01\x00\x01"), "汉汉"; got != want {
		t.Fatalf("Decode = %q, want %q", got, want)
	}
}

func TestToUnicodeSurrogatePairAndMultiChar(t *testing.T) {
	m := mustParseCMap(t, cmapHead+
		"1 begincodespacerange\n<00> <FF>\nendcodespacerange\n"+
		"2 beginbfchar\n<21> <D842DFB6>\n<22> <00660069>\nendbfchar\n"+cmapTail)
	if got, want := m.Decode("\x21\x22"), "𠮶fi"; got != want {
		t.Fatalf("Decode = %q, want %q", got, want)
	}
}

func TestToUnicodeUnmappedCodeBecomesReplacement(t *testing.T) {
	m := mustParseCMap(t, cmapHead+
		"1 begincodespacerange\n<00> <FF>\nendcodespacerange\n"+
		"1 beginbfchar\n<21> <6C49>\nendbfchar\n"+cmapTail)
	if got, want := m.Decode("\x21\x99\x21"), "汉\uFFFD汉"; got != want {
		t.Fatalf("Decode = %q, want %q", got, want)
	}
}

func TestToUnicodeWithoutCodespaceInfersWidth(t *testing.T) {
	m := mustParseCMap(t, cmapHead+"1 beginbfchar\n<21> <6C49>\nendbfchar\n"+cmapTail)
	if got, want := m.Decode("\x21\x21"), "汉汉"; got != want {
		t.Fatalf("Decode = %q, want %q", got, want)
	}
}

func TestToUnicodeRangeLookupIsBinarySearchSafe(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(cmapHead + "1 begincodespacerange\n<00> <FF>\nendcodespacerange\n")
	// 倒序给区间：解析后必须按起点排序，二分才正确。
	sb.WriteString("3 beginbfrange\n<60> <6F> <4E00>\n<40> <4F> <5B57>\n<20> <3F> <6C49>\nendbfrange\n" + cmapTail)
	m := mustParseCMap(t, sb.String())
	if got := m.Decode("\x20"); got != "汉" {
		t.Errorf("0x20 -> %q", got)
	}
	if got := m.Decode("\x40"); got != "字" {
		t.Errorf("0x40 -> %q", got)
	}
	if got := m.Decode("\x60"); got != "一" {
		t.Errorf("0x60 -> %q", got)
	}
	// 0x50 不在任何区间内。
	if got := m.Decode("\x50"); got != "\uFFFD" {
		t.Errorf("0x50 -> %q", got)
	}
}

func TestParseToUnicodeRejectsEmptyAndGarbage(t *testing.T) {
	for name, in := range map[string]string{
		"空":    "",
		"没有映射": cmapHead + cmapTail,
		"垃圾":   "\x00\xff\x7fxx",
	} {
		if parseToUnicode(strings.NewReader(in)) != nil {
			t.Errorf("%s: 应返回 nil", name)
		}
	}
}

type testPDFPage struct {
	toUnicode string // 字体的 ToUnicode CMap 全文
	content   string // 页面内容流
	// plainFont 为真时字体不带 /Encoding（底层库自己用 ToUnicode 解码），否则带无意义字形名的 /Differences。
	plainFont bool
}

// buildTestPDF 手工拼多页 PDF：每页一个字体 F1，Encoding 是带 /Differences 的字典（字形名无意义），文字语义只在 ToUnicode 里。
// 对象号按写入顺序：1 目录，2 页树，之后每页占 4 个（页、内容、字体、ToUnicode）。
func buildTestPDF(pages []testPDFPage) []byte {
	var b bytes.Buffer
	var offs []int
	b.WriteString("%PDF-1.4\n")
	add := func(body string) {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	stream := func(s string) string {
		return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(s), s)
	}
	var kids []string
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 3+4*i))
	}
	add("<< /Type /Catalog /Pages 2 0 R >>")
	add(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	for i, p := range pages {
		base := 3 + 4*i
		add(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>", base+1, base+2))
		add(stream(p.content))
		enc := "/Encoding << /Type /Encoding /Differences [33 /gid1 /gid2 /gid3] >> "
		if p.plainFont {
			enc = ""
		}
		add(fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /Test %s/ToUnicode %d 0 R >>", enc, base+3))
		add(stream(p.toUnicode))
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return b.Bytes()
}

func singleCharCMap(code byte, to string) string {
	return cmapHead + "1 begincodespacerange\n<00> <FF>\nendcodespacerange\n" +
		fmt.Sprintf("1 beginbfchar\n<%02X> <%s>\nendbfchar\n", code, to) + cmapTail
}

// Encoding 带 Differences 且有 ToUnicode 的字体：底层库会按字形名 gid1 之类原样输出字节，要靠 ToUnicode 还原。
func TestExtractPDFUsesToUnicodeForDifferencesFonts(t *testing.T) {
	pdfData := buildTestPDF([]testPDFPage{{
		toUnicode: cmapHead + "1 begincodespacerange\n<00> <FF>\nendcodespacerange\n" +
			"2 beginbfrange\n<21> <21> <6C49>\n<22> <23> <5B57>\nendbfrange\n" + cmapTail,
		content: "BT /F1 12 Tf 72 720 Td <21222321222321> Tj ET",
	}})
	docs, err := Extract(pdfData, "m.pdf")
	if err != nil || len(docs) != 1 {
		t.Fatalf("Extract: docs=%d err=%v", len(docs), err)
	}
	if want := "汉字存汉字存汉"; !strings.Contains(docs[0].Text, want) {
		t.Fatalf("text = %q, want 含 %q", docs[0].Text, want)
	}
}

// 不同页的同名字体（都叫 F1）字形映射不同：底层库的整本提取按资源名跨页缓存解码表，第二页会用第一页的表。
// 这里用不带 Encoding 的字体，让底层库自己走 ToUnicode，才能触发那个缓存。
func TestExtractPDFSameFontNameDifferentMapPerPage(t *testing.T) {
	pdfData := buildTestPDF([]testPDFPage{
		{toUnicode: singleCharCMap(0x21, "6C49"), content: "BT /F1 12 Tf 72 720 Td <212121> Tj ET", plainFont: true},
		{toUnicode: singleCharCMap(0x21, "5B57"), content: "BT /F1 12 Tf 72 720 Td <212121> Tj ET", plainFont: true},
	})
	docs, err := Extract(pdfData, "m.pdf")
	if err != nil || len(docs) != 1 {
		t.Fatalf("Extract: docs=%d err=%v", len(docs), err)
	}
	text := docs[0].Text
	if !strings.Contains(text, "汉汉汉") || !strings.Contains(text, "字字字") {
		t.Fatalf("两页应各用各自的映射，得到 %q", text)
	}
	if strings.Contains(text, "\uFFFD") {
		t.Fatalf("不应出现替换字符: %q", text)
	}
}

// 一页里字体被反复切换，解码结果按页内资源名缓存，不能把后一个字体的内容解成前一个。
func TestExtractPDFFontSwitchWithinPage(t *testing.T) {
	pdfData := buildTestPDF([]testPDFPage{{
		toUnicode: singleCharCMap(0x21, "6C49"),
		content:   "BT /F1 12 Tf 72 720 Td <212121> Tj ET BT /F1 12 Tf 72 700 Td <212121> Tj ET",
	}})
	docs, err := Extract(pdfData, "m.pdf")
	if err != nil || len(docs) != 1 {
		t.Fatalf("Extract: docs=%d err=%v", len(docs), err)
	}
	if strings.Count(docs[0].Text, "汉汉汉") != 2 {
		t.Fatalf("text = %q", docs[0].Text)
	}
}
