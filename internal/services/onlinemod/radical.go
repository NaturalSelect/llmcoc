// NOTE: radical.go 还原 PDF 抽取出的部首字符。部分 PDF 的字体把常用字映射到了「康熙部首」
// （U+2F00–2FDF，如 ⼈ ⼯）和「部首补充」（U+2E80–2EFF，如 ⺠ ⻓）区段，肉眼看和正常汉字一样，
// 但搜索、版权声明正则和 LLM 分词都认不出来。
package onlinemod

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// NOTE: 不对整段文本做 NFKC：它会把全角标点、全角字母数字压成半角，改动正文。这里只对部首区段逐字处理。
const (
	kangxiFirst = 0x2F00
	kangxiLast  = 0x2FDF
)

// supplementRadicals 是 NFKC 也没有分解映射的「部首补充」字符，按 Unicode 字符名对应，
// 并用原文上下文核对过（如「非玩家⻆色」「集⻬」「宝⻉」「⻨乳精」「突⺎」）。
// NOTE: 只收有把握的。U+2E92（蛇部首）同一份原文里既出现在「己巳」「巳初」也出现在「蛇窟」，
// U+2E93 只出现一次无法核对，都不收：留原样好过改错。
var supplementRadicals = map[rune]rune{
	0x2E8E: '兀',
	0x2EA0: '民', 0x2EC1: '虎', 0x2EC4: '西', 0x2EC5: '见', 0x2EC6: '角', 0x2EC9: '贝',
	0x2ECB: '车', 0x2ED3: '长', 0x2ED4: '门', 0x2ED8: '青', 0x2EDA: '页', 0x2EDB: '风',
	0x2EDC: '飞', 0x2EDD: '食', 0x2EE2: '马', 0x2EE3: '骨', 0x2EE4: '鬼', 0x2EE5: '鱼',
	0x2EE6: '鸟', 0x2EE7: '卤', 0x2EE8: '麦', 0x2EE9: '黄', 0x2EEC: '齐', 0x2EEE: '齿',
	0x2EF0: '龙',
}

// normalizeRadicals 把康熙部首和已知的部首补充字符还原成对应的汉字，其余字符原样保留。
func normalizeRadicals(s string) string {
	if !hasRadical(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= kangxiFirst && r <= kangxiLast:
			if out := norm.NFKC.String(string(r)); utf8.RuneCountInString(out) == 1 {
				got, _ := utf8.DecodeRuneInString(out)
				return got
			}
		default:
			if to, ok := supplementRadicals[r]; ok {
				return to
			}
		}
		return r
	}, s)
}

func hasRadical(s string) bool {
	for _, r := range s {
		if r >= 0x2E80 && r <= kangxiLast {
			return true
		}
	}
	return false
}
