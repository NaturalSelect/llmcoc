// NOTE: pdf_decrypt.go 补上文本提取库不支持的 PDF 加密（AES-256，V=5 R=6）。
// 只处理用户密码为空的文件，也就是任何阅读器都能直接打开的那种；需要用户密码的文件仍然报错。
package onlinemod

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// NOTE: 输入来自公网，解密放在带超时的 ctx 里；解密实测百毫秒级，一分钟只是兜底。
const pdfDecryptTimeout = time.Minute

// isUnsupportedEncryption 判断 pdf.NewReader 的错误是否是它不认的加密方式。
// NOTE: 底层库只返回无类型的错误，只能按文案判断；「需要密码」的错误文案是 encrypted，不含 encryption，不会误入。
func isUnsupportedEncryption(err error) bool {
	return err != nil && strings.Contains(err.Error(), "encryption")
}

// decryptPDF 用空密码解开加密的 PDF，返回不再加密的文件字节。
// NOTE: 用无状态配置：默认配置会在 ~/.config 下建目录，目录不可写时直接 panic。
func decryptPDF(data []byte) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%v", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), pdfDecryptTimeout)
	defer cancel()
	var buf bytes.Buffer
	if err := api.Decrypt(ctx, bytes.NewReader(data), &buf, model.NewStatelessConfiguration()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
