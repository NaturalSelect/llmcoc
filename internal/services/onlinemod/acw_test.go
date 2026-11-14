// NOTE: acw_test.go 验证 solveACW。期望值不来自实现的输出：
// 全 0 / 全 F / 单字符探针由手算（置换表查位置、掩码逐半字节异或）得出，
// 其余向量由另写的 Python 脚本用「输出第 j 位取输入第 posList[j] 位」的反查写法独立算出
// （实现用的是「输入第 i 位推到 posList 里等于 i+1 的下标」的正查写法）。
// 这些测试只能说明实现与已知算法一致，不能证明算法常量对真实站点有效。
package onlinemod

import (
	"regexp"
	"strings"
	"testing"
)

func TestSolveACW(t *testing.T) {
	const (
		mask = "3000176000856006061501533003690027800375"
		// 掩码每个半字节与 F 异或的结果：3→c 0→f 1→e 7→8 6→9 8→7 5→a 2→d 9→6。
		allF = "cfffe89fff7a9ff9f9eafeaccffc96ffd87ffc8a"
	)
	tests := []struct {
		name string
		arg1 string
		want string
	}{
		// 全 0：置换后仍是全 0，结果就是掩码本身，单独检验异或这一步。
		{"全0等于掩码", strings.Repeat("0", 40), mask},
		// 全 F：置换不改变内容，结果是掩码按位取反，同样只检验异或。
		{"全F等于掩码取反", strings.Repeat("F", 40), allF},
		// 探针：只有 arg1 的一个位置是 F，用来检验置换。
		// arg1[14] 是第 15 个字符，posList[0]=15，落在 unsbox[0]，即第 0 字节高半字节：0x30 的高半字节 3^F=c。
		{"探针arg1[14]落到unsbox[0]", probe(14), "c000176000856006061501533003690027800375"},
		// arg1[34] 是第 35 个字符，posList[1]=35，落在 unsbox[1]，即第 0 字节低半字节：0^F=f，得 0x3f。
		{"探针arg1[34]落到unsbox[1]", probe(34), "3f00176000856006061501533003690027800375"},
		// arg1[0] 是第 1 个字符，posList[6]=1，落在 unsbox[6]，即第 3 字节高半字节：0x60 的 6^F=9。
		{"探针arg1[0]落到unsbox[6]", probe(0), "3000179000856006061501533003690027800375"},
		// arg1[39] 是第 40 个字符，posList[12]=40，落在 unsbox[12]，即第 6 字节高半字节：0x60 的 6^F=9。
		{"探针arg1[39]落到unsbox[12]", probe(39), "3000176000859006061501533003690027800375"},
		{"混合向量", "3A7F9C21B0D4E85F6A13C97D2E4B80F15C6A39D7", "668d48590b9a149128d8db62bc1dc92f4efbc03f"},
		{"小写输入与大写等价", "3a7f9c21b0d4e85f6a13c97d2e4b80f15c6a39d7", "668d48590b9a149128d8db62bc1dc92f4efbc03f"},
		{"长度不足", strings.Repeat("A", 39), ""},
		{"长度过长", strings.Repeat("A", 41), ""},
		{"含非十六进制字符", strings.Repeat("A", 39) + "G", ""},
		{"空串", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := solveACW(tt.arg1); got != tt.want {
				t.Fatalf("solveACW(%q)\n got  %q\n want %q", tt.arg1, got, tt.want)
			}
		})
	}
}

// probe 返回只有第 k 位（0 起）是 F、其余为 0 的 40 位串。
func probe(k int) string {
	b := []byte(strings.Repeat("0", 40))
	b[k] = 'F'
	return string(b)
}

// 置换必须是双射：40 个单字符探针各自只改变结果的一个半字节，且落点互不相同。
func TestSolveACWPermutationIsBijection(t *testing.T) {
	zero := solveACW(strings.Repeat("0", 40))
	seen := map[int]int{}
	for k := 0; k < 40; k++ {
		got := solveACW(probe(k))
		if len(got) != 40 {
			t.Fatalf("探针 %d 的结果长度 = %d", k, len(got))
		}
		diff := -1
		for i := range got {
			if got[i] != zero[i] {
				if diff >= 0 {
					t.Fatalf("探针 %d 改变了不止一个半字节", k)
				}
				diff = i
			}
		}
		if diff < 0 {
			t.Fatalf("探针 %d 没有改变结果", k)
		}
		if prev, dup := seen[diff]; dup {
			t.Fatalf("探针 %d 与 %d 落在同一位置 %d", k, prev, diff)
		}
		seen[diff] = k
	}
}

func TestSolveACWOutputShape(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, in := range []string{"3A7F9C21B0D4E85F6A13C97D2E4B80F15C6A39D7", strings.Repeat("F", 40), probe(7)} {
		if got := solveACW(in); !re.MatchString(got) {
			t.Fatalf("solveACW(%q) = %q，应为 40 位小写十六进制", in, got)
		}
	}
}

func TestACWArg1Extraction(t *testing.T) {
	page := `<html><script>var arg1='3A7F9C21B0D4E85F6A13C97D2E4B80F15C6A39D7';var _0x4818=['x'];</script></html>`
	m := reACWArg1.FindStringSubmatch(page)
	if m == nil || m[1] != "3A7F9C21B0D4E85F6A13C97D2E4B80F15C6A39D7" {
		t.Fatalf("arg1 提取失败: %v", m)
	}
	if reACWArg1.MatchString(`var arg1='ZZ';`) {
		t.Fatal("非十六进制的 arg1 不应匹配")
	}
}

func TestLooksLikeHTML(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{"doctype", "<!DOCTYPE html><html>", true},
		{"前导空白与BOM", "\xef\xbb\xbf \r\n<html lang=zh>", true},
		{"script开头", "<script>var arg1='x'</script>", true},
		{"zip", "PK\x03\x04abc", false},
		{"pdf", "%PDF-1.4", false},
		{"纯文本", "hello", false},
		{"空", "", false},
	}
	for _, tt := range tests {
		if got := looksLikeHTML([]byte(tt.data)); got != tt.want {
			t.Errorf("%s: looksLikeHTML = %v, want %v", tt.name, got, tt.want)
		}
	}
}
