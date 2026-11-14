// NOTE: license.go 做「禁止投喂 AI」类声明的筛查，规则照搬 skill 文档 convert.md：
// 先删光全部空白（逐字换行的 PDF 才能命中），再用正则取命中片段。
// 命中只是线索，「投喂食物」之类的误报需要人工看上下文判断。
package onlinemod

import "regexp"

// maxLicenseHits 限制返回的片段数，避免整篇都是声明时撑大响应。
const maxLicenseHits = 20

var (
	// NOTE: Go 的 \s 只含 ASCII 空白，补上 \p{Z} 才能去掉全角空格和 NBSP，中文文档常用它们做缩进。
	reLicenseSpace = regexp.MustCompile(`[\s\p{Z}]+`)
	// NOTE: 空白已删除，所以 . 不匹配换行也不会截断片段；(?i) 让 ai 不分大小写，{m,n} 按 rune 计。
	reLicense = regexp.MustCompile(`(?i).{0,24}(?:投喂|喂养|导入|输入|训练|学习)[^。；\n]{0,8}(?:ai|人工智能).{0,16}|.{0,20}(?:禁止|不得|不可|请勿)[^。；\n]{0,12}(?:ai|人工智能).{0,12}`)
)

// ScanLicense 返回疑似「禁止投喂 AI」声明的片段，去重，最多 20 条；没有命中时返回空切片而不是 nil。
func ScanLicense(text string) []string {
	hits := []string{}
	flat := reLicenseSpace.ReplaceAllString(text, "")
	seen := map[string]bool{}
	for _, m := range reLicense.FindAllString(flat, -1) {
		if seen[m] {
			continue
		}
		seen[m] = true
		hits = append(hits, m)
		if len(hits) >= maxLicenseHits {
			break
		}
	}
	return hits
}
