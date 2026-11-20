package agent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// companionNameProvider 只实现 Chat：翻译请求返回 translated，其余取名请求返回 created。
type companionNameProvider struct {
	llm.Provider
	created    string
	translated string
	calls      int
}

func (p *companionNameProvider) Chat(_ context.Context, _ string, msgs []llm.ChatMessage) (string, error) {
	p.calls++
	if strings.Contains(msgs[len(msgs)-1].Content, "译成中文") {
		return p.translated, nil
	}
	return p.created, nil
}

func TestIsChineseName(t *testing.T) {
	cases := map[string]bool{
		"陈守拙":      true,
		"爱德华·史密斯": true,
		"佐々木亮":     true,
		"":         false,
		"陈":        false,
		"Edward":   false,
		"中村さくら":    false,
		"陈守 拙":     false,
		"陈守拙2":     false,
	}
	for name, want := range cases {
		if got := isChineseName(name); got != want {
			t.Errorf("isChineseName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestToChineseName(t *testing.T) {
	ctx := context.Background()

	t.Run("汉字姓名原样返回且不调用LLM", func(t *testing.T) {
		prov := &nameChatProvider{names: []string{"不该被用到"}}
		got, err := toChineseName(ctx, newWriterTestHandle(prov), "陈守拙")
		if err != nil || got != "陈守拙" || prov.calls != 0 {
			t.Fatalf("got %q err=%v calls=%d, want 陈守拙 with no LLM call", got, err, prov.calls)
		}
	})

	t.Run("外文姓名经LLM译成中文", func(t *testing.T) {
		prov := &nameChatProvider{names: []string{"爱德华·史密斯"}}
		got, err := toChineseName(ctx, newWriterTestHandle(prov), "Edward Smith")
		if err != nil || got != "爱德华·史密斯" {
			t.Fatalf("got %q err=%v, want 爱德华·史密斯", got, err)
		}
	})

	t.Run("译名仍含外文字符时报错", func(t *testing.T) {
		prov := &nameChatProvider{names: []string{"Edward"}}
		if got, err := toChineseName(ctx, newWriterTestHandle(prov), "Edward Smith"); err == nil {
			t.Fatalf("got %q, want error for non-Chinese translation", got)
		}
	})

	t.Run("LLM不可用时外文姓名报错", func(t *testing.T) {
		if got, err := toChineseName(ctx, agentHandle{}, "Edward Smith"); err == nil {
			t.Fatalf("got %q, want error without LLM", got)
		}
	})
}

func TestPickCompanionCardName(t *testing.T) {
	ctx := context.Background()

	t.Run("无效性别", func(t *testing.T) {
		if _, err := pickCompanionCardName(ctx, agentHandle{}, "未知", nil); err == nil {
			t.Fatal("want error for invalid gender")
		}
	})

	t.Run("没有LLM时也只给中文姓名", func(t *testing.T) {
		for i := 0; i < 40; i++ {
			for _, gender := range []string{models.GenderMale, models.GenderFemale} {
				name, err := pickCompanionCardName(ctx, agentHandle{}, gender, nil)
				if err != nil || !isChineseName(name) {
					t.Fatalf("name=%q err=%v, want a Chinese name", name, err)
				}
			}
		}
	})

	t.Run("无论抽到哪种文化都经LLM译成中文，已用姓名改取静态池", func(t *testing.T) {
		prov := &companionNameProvider{created: "Taro Yamada", translated: "爱德华·史密斯"}
		handle := newWriterTestHandle(prov)
		used := map[string]bool{}

		first, err := pickCompanionCardName(ctx, handle, models.GenderMale, used)
		if err != nil || first != "爱德华·史密斯" {
			t.Fatalf("first = %q err=%v, want 爱德华·史密斯", first, err)
		}
		markNPCNamesUsed(used, first)

		second, err := pickCompanionCardName(ctx, handle, models.GenderMale, used)
		if err != nil {
			t.Fatalf("second err = %v", err)
		}
		if second == first || !isChineseName(second) {
			t.Fatalf("second = %q, want a different Chinese name", second)
		}
		if !slices.Contains(npcNameFallbackPool["chinese"]["male"], second) {
			t.Fatalf("second = %q, want a name from the static Chinese pool", second)
		}
	})
}
