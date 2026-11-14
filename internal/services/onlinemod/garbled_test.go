package onlinemod

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestLooksGarbled(t *testing.T) {
	zh := strings.Repeat("调查员在雨夜抵达庄园。", 40)
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"空", "", false},
		{"正常中文", zh, false},
		{"正常英文", strings.Repeat("The investigators arrive at the manor. ", 20), false},
		{"全是替换字符", strings.Repeat("\ufffd", 300), true},
		{"Latin-1 乱码", strings.Repeat("µëïτ_σ_êΣ", 40), true},
		{"控制字符", strings.Repeat("\x01\x02\x03", 100), true},
		{"样本太短不判", strings.Repeat("\ufffd", garbledMinRunes-1), false},
		{"空白不计入样本", strings.Repeat("\ufffd ", garbledMinRunes-1), false},
		{"零星符号不算乱码", zh + "×°·é", false},
		{"部分字符缺映射", strings.Repeat("调查员在雨夜抵达庄园，门口有人。", 30) + strings.Repeat("\ufffd", 30), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksGarbled(tt.text); got != tt.want {
				t.Fatalf("looksGarbled = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLooksGarbledThresholdBoundary(t *testing.T) {
	// 1000 个非空白字符，恰好 3% 乱码不判，超过 3% 才判。
	build := func(junk int) string {
		return strings.Repeat("文", 1000-junk) + strings.Repeat("\ufffd", junk)
	}
	if looksGarbled(build(30)) {
		t.Fatal("恰好 3% 不应判乱码")
	}
	if !looksGarbled(build(31)) {
		t.Fatal("超过 3% 应判乱码")
	}
}

func TestNeedsOCR(t *testing.T) {
	if !needsOCR("") || !needsOCR(" \n\t ") {
		t.Fatal("没有文字应需要 OCR")
	}
	if !needsOCR(strings.Repeat("\ufffd", 300)) {
		t.Fatal("乱码应需要 OCR")
	}
	if needsOCR("很短但是正常的文字") {
		t.Fatal("正常文字不需要 OCR")
	}
}

type fakeOCR struct {
	mu    sync.Mutex
	calls []string
	text  string
	err   error
}

func (f *fakeOCR) ParsePDF(_ context.Context, name string, _ []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	return f.text, f.err
}

// garbledPDF 的每个字符码都不在 ToUnicode 里，本地提取只能得到替换字符。
func garbledPDF() []byte {
	return buildTestPDF([]testPDFPage{{
		toUnicode: singleCharCMap(0x22, "6C49"),
		content:   "BT /F1 12 Tf 72 720 Td <" + strings.Repeat("21", 300) + "> Tj ET",
	}})
}

func healthyPDF() []byte {
	return buildTestPDF([]testPDFPage{{
		toUnicode: singleCharCMap(0x21, "6C49"),
		content:   "BT /F1 12 Tf 72 720 Td <" + strings.Repeat("21", 300) + "> Tj ET",
	}})
}

func TestExtractWithOCRReplacesGarbledPDF(t *testing.T) {
	ocr := &fakeOCR{text: "# 模组\n\n" + strings.Repeat("调查员抵达庄园。", 40)}
	docs, err := ExtractWithOCR(context.Background(), garbledPDF(), "dir/鬼屋.pdf", ocr)
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs=%d err=%v", len(docs), err)
	}
	d := docs[0]
	if !d.OCR || d.Garbled {
		t.Fatalf("OCR=%v Garbled=%v, want true/false", d.OCR, d.Garbled)
	}
	if !strings.Contains(d.Text, "调查员抵达庄园") || strings.Contains(d.Text, "\ufffd") {
		t.Fatalf("应使用 OCR 文字: %q", d.Text[:40])
	}
	if len(ocr.calls) != 1 || ocr.calls[0] != "鬼屋.pdf" {
		t.Fatalf("OCR 调用 = %v，应只传文件名", ocr.calls)
	}
}

func TestExtractWithOCRKeepsHealthyPDF(t *testing.T) {
	ocr := &fakeOCR{text: "不该被用到"}
	docs, err := ExtractWithOCR(context.Background(), healthyPDF(), "m.pdf", ocr)
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs=%d err=%v", len(docs), err)
	}
	if docs[0].OCR || docs[0].Garbled || len(ocr.calls) != 0 {
		t.Fatalf("正常 PDF 不应走 OCR: OCR=%v Garbled=%v calls=%v", docs[0].OCR, docs[0].Garbled, ocr.calls)
	}
}

func TestExtractWithOCRFailureKeepsLocalTextAndMarksGarbled(t *testing.T) {
	cases := map[string]*fakeOCR{
		"OCR 出错":     {err: errors.New("boom")},
		"OCR 返回仍是乱码": {text: strings.Repeat("\ufffd", 300)},
		"OCR 返回空":    {text: "  "},
	}
	for name, ocr := range cases {
		t.Run(name, func(t *testing.T) {
			docs, err := ExtractWithOCR(context.Background(), garbledPDF(), "m.pdf", ocr)
			if err != nil || len(docs) != 1 {
				t.Fatalf("docs=%d err=%v", len(docs), err)
			}
			if docs[0].OCR || !docs[0].Garbled || docs[0].Candidate {
				t.Fatalf("OCR=%v Garbled=%v Candidate=%v", docs[0].OCR, docs[0].Garbled, docs[0].Candidate)
			}
			if len(ocr.calls) != 1 {
				t.Fatalf("OCR 调用次数 = %d", len(ocr.calls))
			}
		})
	}
}

func TestExtractWithoutOCRMarksGarbled(t *testing.T) {
	docs, err := Extract(garbledPDF(), "m.pdf")
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs=%d err=%v", len(docs), err)
	}
	if !docs[0].Garbled || docs[0].OCR {
		t.Fatalf("没有 OCR 时乱码应被标记: Garbled=%v OCR=%v", docs[0].Garbled, docs[0].OCR)
	}
}

func TestGarbledDocumentIsNeverCandidate(t *testing.T) {
	// 本地文字很长、乱码比例高：即使字数和体积都够，也不能当候选正文。
	var content strings.Builder
	content.WriteString("BT /F1 12 Tf 72 720 Td <")
	content.WriteString(strings.Repeat("21", 4000))
	content.WriteString("> Tj ET")
	data := buildTestPDF([]testPDFPage{{toUnicode: singleCharCMap(0x22, "6C49"), content: content.String()}})
	docs, err := Extract(data, "m.pdf")
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs=%d err=%v", len(docs), err)
	}
	if docs[0].Runes <= candidateMinRunes || !docs[0].Garbled || docs[0].Candidate {
		t.Fatalf("Runes=%d Garbled=%v Candidate=%v", docs[0].Runes, docs[0].Garbled, docs[0].Candidate)
	}
}

func TestExtractWithOCRLimitsCallsPerArchive(t *testing.T) {
	var ents []zipEnt
	for i := 0; i < maxOCRPerArchive+2; i++ {
		ents = append(ents, zipEnt{name: strings.Repeat("a", i+1) + ".pdf", body: garbledPDF()})
	}
	ocr := &fakeOCR{err: errors.New("boom")}
	docs, err := ExtractWithOCR(context.Background(), buildZip(t, ents...), "x.zip", ocr)
	if err != nil || len(docs) != len(ents) {
		t.Fatalf("docs=%d err=%v", len(docs), err)
	}
	if len(ocr.calls) != maxOCRPerArchive {
		t.Fatalf("OCR 调用 %d 次，上限 %d", len(ocr.calls), maxOCRPerArchive)
	}
	for _, d := range docs {
		if !d.Garbled {
			t.Fatalf("%s 应被标记乱码", d.Path)
		}
	}
}

func TestExtractWithOCRSkipsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ocr := &fakeOCR{text: strings.Repeat("调查员抵达庄园。", 40)}
	docs, err := ExtractWithOCR(ctx, garbledPDF(), "m.pdf", ocr)
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs=%d err=%v", len(docs), err)
	}
	if len(ocr.calls) != 0 || !docs[0].Garbled {
		t.Fatalf("ctx 已取消不应调用 OCR: calls=%v Garbled=%v", ocr.calls, docs[0].Garbled)
	}
}

func TestExtractWithOCRDoesNotTouchNonPDF(t *testing.T) {
	ocr := &fakeOCR{text: "不该被用到"}
	docs, err := ExtractWithOCR(context.Background(), []byte(strings.Repeat("\ufffd", 300)), "a.txt", ocr)
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs=%d err=%v", len(docs), err)
	}
	if len(ocr.calls) != 0 || docs[0].OCR || docs[0].Garbled {
		t.Fatalf("txt 不走 OCR: calls=%v OCR=%v Garbled=%v", ocr.calls, docs[0].OCR, docs[0].Garbled)
	}
}
