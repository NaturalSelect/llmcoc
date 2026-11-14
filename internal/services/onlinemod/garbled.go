// NOTE: garbled.go 判断 PDF 本地抽取出的文本能不能用：没有文字（扫描件）或大面积乱码
// （字体没有可用的字符映射时会得到 Latin-1 / 希腊字母 / 控制字符 / U+FFFD）都需要 OCR 兜底。
package onlinemod

import (
	"strings"
	"unicode"
)

const (
	// garbledMinRunes 是参与判定的最少非空白字符数，太短的文本样本不足，不判乱码。
	garbledMinRunes = 200
	// garbledJunkPercent 是「乱码字符」占非空白字符的百分比上限，超过即判乱码。
	// NOTE: 用 70 多份真实模组 PDF 标定：正常文件最高约 1.6%，部分字符缺映射的文件约 5%，
	// 完全乱码的文件在 40% 以上。取 3% 把部分缺映射的也算进来。
	garbledJunkPercent = 3
)

// looksGarbled 判断文本是否有相当比例由乱码字符构成。只看字符类别，不依赖语种，英文正文不会误判。
func looksGarbled(text string) bool {
	total, junk := 0, 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if isJunkRune(r) {
			junk++
		}
	}
	return total >= garbledMinRunes && junk*100 > total*garbledJunkPercent
}

// needsOCR 判断本地抽取的 PDF 文本是否不可用：完全没有文字，或者乱码。
func needsOCR(text string) bool {
	return strings.TrimSpace(text) == "" || looksGarbled(text)
}

// isJunkRune 判断一个字符是不是字体解码失败的典型产物。
func isJunkRune(r rune) bool {
	switch {
	case r == '\ufffd', unicode.IsControl(r):
		return true
	case r >= 0x80 && r <= 0xff: // Latin-1 补充：字节被当成 Latin-1 解出来的常见形态
		return true
	case r >= 0x370 && r <= 0x3ff: // 希腊字母：GBK 字节按 CP437 解出来的常见形态
		return true
	case r >= 0x2500 && r <= 0x259f: // 制表符和方块
		return true
	case r >= 0xe000 && r <= 0xf8ff: // 私用区
		return true
	}
	return false
}
