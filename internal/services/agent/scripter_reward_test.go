// NOTE: scripter_reward_test.go 验证 fallbackRewardConcept——compiler 两次提交
// reward_concept 仍为空时，触发点用于合成兜底奖励概念的纯函数。
package agent

import (
	"strings"
	"testing"
)

func TestFallbackRewardConcept(t *testing.T) {
	anchor := "食尸鬼（Ghoul）"
	got := fallbackRewardConcept(anchor)
	if strings.TrimSpace(got) == "" {
		t.Fatal("fallbackRewardConcept 不应返回空字符串")
	}
	if !strings.Contains(got, anchor) {
		t.Errorf("fallbackRewardConcept(%q) = %q，应包含 mythos_anchor 以保证兜底概念与本篇神话锚点相关", anchor, got)
	}
	if got := fallbackRewardConcept(""); got != "" {
		t.Errorf("mythos_anchor 为空时 fallbackRewardConcept 应返回空字符串, got %q", got)
	}
	if got := fallbackRewardConcept("   "); got != "" {
		t.Errorf("mythos_anchor 全为空白字符时 fallbackRewardConcept 应返回空字符串, got %q", got)
	}
}
