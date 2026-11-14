// NOTE: pdf_decrypt_test.go 现场用 pdfcpu 把 minimalPDF 加密成 AES-256，再验证提取流程。
package onlinemod

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func encryptPDFAES256(t *testing.T, plain []byte, userPW, ownerPW string) []byte {
	t.Helper()
	conf := model.NewStatelessConfiguration()
	conf.UserPW, conf.OwnerPW = userPW, ownerPW
	conf.EncryptUsingAES, conf.EncryptKeyLength = true, 256
	var out bytes.Buffer
	if err := api.Encrypt(context.Background(), bytes.NewReader(plain), &out, conf); err != nil {
		t.Fatalf("加密测试 PDF 失败: %v", err)
	}
	return out.Bytes()
}

func TestPDFTextAES256EmptyUserPassword(t *testing.T) {
	enc := encryptPDFAES256(t, minimalPDF("Hello Encrypted Module"), "", "owner")
	// 先确认底层库确实读不了，否则这个用例没有测到解密回退。
	if _, err := pdf.NewReader(bytes.NewReader(enc), int64(len(enc))); !isUnsupportedEncryption(err) {
		t.Fatalf("pdf.NewReader err = %v，测试 PDF 应触发不支持的加密", err)
	}
	text, err := pdfText(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Hello Encrypted Module") {
		t.Fatalf("text = %q", text)
	}
}

func TestExtractAES256EmptyUserPasswordInZip(t *testing.T) {
	enc := encryptPDFAES256(t, minimalPDF("Hello Zip Module"), "", "owner")
	docs, err := Extract(makeZip(t, map[string]string{"mod.pdf": string(enc)}), "mod.zip")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || !strings.Contains(docs[0].Text, "Hello Zip Module") {
		t.Fatalf("docs = %+v", docs)
	}
}

// 需要用户密码的 PDF 不能被绕过：报错，不 panic，错误里带上解密失败的说明。
func TestPDFTextAES256NeedsUserPassword(t *testing.T) {
	enc := encryptPDFAES256(t, minimalPDF("Secret"), "user-secret", "owner")
	text, err := pdfText(enc)
	if err == nil {
		t.Fatalf("需要用户密码的 PDF 应报错，得到 %q", text)
	}
	if !strings.Contains(err.Error(), "用空密码解密也失败") {
		t.Errorf("err = %v，应说明空密码解密失败", err)
	}
}

func TestIsUnsupportedEncryption(t *testing.T) {
	for _, tc := range []struct {
		err  string
		want bool
	}{
		{"malformed PDF: 256-bit encryption key", true},
		{"unsupported PDF: encryption version V=5", true},
		{"unsupported PDF: encryption revision R=6", true},
		{"encrypted PDF: invalid password", false},
		{"not a PDF file: invalid header", false},
	} {
		if got := isUnsupportedEncryption(errString(tc.err)); got != tc.want {
			t.Errorf("isUnsupportedEncryption(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
	if isUnsupportedEncryption(nil) {
		t.Error("nil 不是加密错误")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
