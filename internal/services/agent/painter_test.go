// NOTE: 覆盖 normalizeImageAspect——Director 传入的 aspect 是 LLM 不可信输入，
// 非法/未识别值必须在这个边界回落方图，不能透传给 Provider。
package agent

import "testing"

func TestNormalizeImageAspect(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"landscape 原样通过", "landscape", "landscape"},
		{"portrait 原样通过", "portrait", "portrait"},
		{"square 原样通过", "square", "square"},
		{"大写归一化", "Landscape", "landscape"},
		{"前后空格归一化", "  portrait  ", "portrait"},
		{"空字符串回落方图", "", "square"},
		{"非法值回落方图", "diagonal", "square"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeImageAspect(tt.raw)
			if got != tt.want {
				t.Errorf("normalizeImageAspect(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
