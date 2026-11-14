// NOTE: radical_test.go 的码点与期望字由 Unicode 字符名核对（python unicodedata），不是抄实现的输出：
// U+2F08 KANGXI RADICAL MAN=人，U+2F4C KANGXI RADICAL STOP=止，U+2F2F KANGXI RADICAL WORK=工，
// U+2EA0 CJK RADICAL CIVILIAN=民，U+2ED3 CJK RADICAL C-SIMPLIFIED LONG=长，U+2EC6 CJK RADICAL SIMPLIFIED HORN=角。
package onlinemod

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeRadicals(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"康熙部首", "\u2f08\u2f2f", "人工"},
		{"部首补充", "\u2ea0\u2ed3\u2ec6", "民长角"},
		{"夹在正常文字里", "非玩家\u2ec6色，\u2ea0不聊生", "非玩家角色，民不聊生"},
		{"没有部首原样返回", "调查员 A，ＡＢＣ１２３！「」…", "调查员 A，ＡＢＣ１２３！「」…"},
		{"全角标点和字母不被压成半角", "（甲）：ＡＩ，", "（甲）：ＡＩ，"},
		{"蛇部首有歧义不动", "己\u2e92日", "己\u2e92日"},
		{"空串", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeRadicals(tc.in); got != tc.want {
				t.Fatalf("normalizeRadicals(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// 覆盖整个康熙部首区段：每个字符要么还原成单个汉字，要么原样保留，不能变成多个字符或丢失。
func TestNormalizeRadicalsKangxiBlockNeverExpands(t *testing.T) {
	for r := rune(kangxiFirst); r <= kangxiLast; r++ {
		got := normalizeRadicals(string(r))
		if utf8.RuneCountInString(got) != 1 {
			t.Errorf("U+%04X 还原成 %q，不是单个字符", r, got)
		}
	}
}

// 映射表本身的健全性：源在部首补充区段，目标是常用汉字区段，且目标字符都是 3 字节（长度不变是 extract 里先截断后规范化的前提）。
func TestSupplementRadicalsTable(t *testing.T) {
	for from, to := range supplementRadicals {
		if from < 0x2E80 || from > 0x2EFF {
			t.Errorf("U+%04X 不在部首补充区段", from)
		}
		if to < 0x4E00 || to > 0x9FFF {
			t.Errorf("U+%04X -> %q 不是基本汉字", from, to)
		}
		if utf8.RuneLen(from) != utf8.RuneLen(to) {
			t.Errorf("U+%04X -> %q 字节数不同", from, to)
		}
	}
}

// 部首写法的版权声明，规范化之前正则认不出，之后要能命中。
// 「不得」的「不」没有康熙部首形式，所以用「禁止」：U+2F4C=止，且 reLicense 的两条分支都要求
// 「禁止/不得/不可/请勿」或「投喂…」这些词字面出现，把「止」换成部首后「禁止」就不再匹配。
func TestExtractNormalizesRadicalsSoLicenseScanHits(t *testing.T) {
	declaration := "本模组禁\u2f4c\u2f64AI"
	if hits := ScanLicense(declaration); len(hits) != 0 {
		t.Fatalf("前提不成立：部首写法本来就能命中 %q", hits)
	}
	data := buildDocx(t, para(declaration)+para(cjk(3500)))
	docs, err := Extract(data, "m.docx")
	if err != nil || len(docs) != 1 {
		t.Fatalf("Extract: docs=%d err=%v", len(docs), err)
	}
	if !strings.Contains(docs[0].Text, "本模组禁止用AI") {
		t.Fatalf("正文没有还原部首: %q", docs[0].Text[:60])
	}
	if hits := ScanLicense(docs[0].Text); len(hits) != 1 || !strings.Contains(hits[0], "禁止用AI") {
		t.Fatalf("hits = %q", hits)
	}
}

// 截断发生在规范化之前，部首和汉字同为 3 字节，所以字数和截断说明都不受影响。
func TestExtractRadicalsDoNotChangeRuneCount(t *testing.T) {
	body := strings.Repeat("\u2ea0", 4000)
	data := buildDocx(t, para(body))
	docs, err := Extract(data, "m.docx")
	if err != nil || len(docs) != 1 {
		t.Fatalf("Extract: docs=%d err=%v", len(docs), err)
	}
	if docs[0].Runes != 4000 || docs[0].Text != strings.Repeat("民", 4000) {
		t.Fatalf("runes=%d text 前 12 字节=%q", docs[0].Runes, docs[0].Text[:12])
	}
}
