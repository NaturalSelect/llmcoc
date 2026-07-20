// NOTE: 落盘前对图片做有损重编码，减小磁盘占用和传输体积。
package imagestore

import (
	"bytes"
	"image"
	"image/jpeg"
	_ "image/png" // 注册 PNG 解码器供 image.Decode 使用
	"log"
	"os"
	"strconv"
	"strings"
)

const defaultImageQuality = 85

// imageQuality 读取 LLMCOC_IMAGE_QUALITY 环境变量，取值范围 1-100，默认 85。
// 设为 0 或负值可关闭压缩。
func imageQuality() int {
	raw := strings.TrimSpace(os.Getenv("LLMCOC_IMAGE_QUALITY"))
	if raw == "" {
		return defaultImageQuality
	}
	q, err := strconv.Atoi(raw)
	if err != nil {
		return defaultImageQuality
	}
	if q <= 0 {
		return 0
	}
	if q > 100 {
		return 100
	}
	return q
}

// CompressImage 尝试把图片重新编码为体积更小的 JPEG。
// srcMIME 为 image/webp 时直接原样返回（Go 无 WebP 编码器，且 WebP 本身压缩率已较高）。
// 解码失败（非法/损坏图片）或重编码后体积未变小时，同样原样返回。
// changed 为 true 表示 dst 是压缩后的新字节，需要用它替换原始数据落盘。
func CompressImage(raw []byte, srcMIME string) (dst []byte, dstMIME string, dstExt string, changed bool) {
	srcMIME, srcExt, ok := NormalizeMIME(srcMIME)
	if !ok {
		return raw, srcMIME, srcExt, false
	}
	quality := imageQuality()
	if quality <= 0 || srcMIME == "image/webp" {
		return raw, srcMIME, srcExt, false
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		log.Printf("[images] decode for compression failed, keep original: %v", err)
		return raw, srcMIME, srcExt, false
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		log.Printf("[images] jpeg encode failed, keep original: %v", err)
		return raw, srcMIME, srcExt, false
	}

	// JPEG 重新编码是同格式压缩，若结果没有变小说明原图已经足够紧凑，保留原样；
	// PNG（无损）转 JPEG（有损）是格式转换，体积几乎必然更小，无需比较直接采用。
	if srcMIME == "image/jpeg" && buf.Len() >= len(raw) {
		return raw, srcMIME, srcExt, false
	}
	return buf.Bytes(), "image/jpeg", ".jpg", true
}
