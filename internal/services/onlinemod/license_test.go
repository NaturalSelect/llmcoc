// NOTE: license_test.go 覆盖版权声明筛查。期望由正则语义手工推出：空白先被删光，
// 所以逐字换行也能命中；误报样例里「投喂」后面 8 个字内没有 ai，且没有禁止类词，不应命中。
package onlinemod

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestScanLicenseHits(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string // 期望的命中片段；nil 表示不应命中
	}{
		{"逐字换行", "禁\n止\n投\n喂\nAI", []string{"禁止投喂AI"}},
		{"整句", "本模组禁止将本模组投喂给AI。", []string{"本模组禁止将本模组投喂给AI。"}},
		{"全角空格与NBSP", "禁止\u3000投喂\u00a0AI", []string{"禁止投喂AI"}},
		{"小写ai", "请勿投喂ai", []string{"请勿投喂ai"}},
		{"人工智能", "不得将本文用于训练人工智能模型", []string{"不得将本文用于训练人工智能模型"}},
		{"误报样例_投喂食物", "PL 经过时需潜行困难或投喂食物", nil},
		{"误报样例_投喂牲畜", "给大蛇投喂牲畜", nil},
		{"商用限制不算", "不可商用、禁止二传", nil},
		{"句号隔断", "禁止投喂。AI", nil},
		{"空文本", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScanLicense(tt.text)
			if got == nil {
				t.Fatal("ScanLicense 不应返回 nil")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("命中 %q，期望 %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("第 %d 条 = %q，期望 %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestScanLicenseEmptyIsJSONArray(t *testing.T) {
	b, err := json.Marshal(ScanLicense("什么都没有"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Fatalf("序列化 = %s，期望 []", b)
	}
}

// 同一句声明在文中重复出现、上下文相同，只保留一条。
// 每处声明前后各有 30 个无关字符：前缀最多取 22 个字（24 减去「禁止」），后缀取 16 个字，
// 相邻两次命中互不重叠，且片段完全相同。
func TestScanLicenseDedupe(t *testing.T) {
	block := strings.Repeat("甲", 30) + "禁止投喂AI" + strings.Repeat("乙", 30)
	hits := ScanLicense(strings.Repeat(block, 5))
	if len(hits) != 1 {
		t.Fatalf("重复声明应去重为 1 条，得到 %d 条: %q", len(hits), hits)
	}
}

func TestScanLicenseCap(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		// 编号放在声明前 24 个字以内，保证每条片段都不同。
		fmt.Fprintf(&sb, "%s%03d禁止投喂AI%s", strings.Repeat("甲", 30), i, strings.Repeat("乙", 30))
	}
	hits := ScanLicense(sb.String())
	if len(hits) != maxLicenseHits {
		t.Fatalf("命中数 = %d，期望上限 %d", len(hits), maxLicenseHits)
	}
}
