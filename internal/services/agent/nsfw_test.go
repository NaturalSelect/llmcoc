package agent

import (
	"strings"
	"testing"
)

// 校验各 agent 提示词在 NSFW 开关两种状态下的渲染结果:
// 开启必须与旧版硬编码文案逐字一致;关闭必须替换且不得残留占位符。
func TestRenderNSFW(t *testing.T) {
	tests := []struct {
		name      string
		tpl       string
		onWanted  []string
		offWanted []string
	}{
		{
			name: "director",
			tpl:  kpSystemPrompt,
			onWanted: []string{
				"<nsfw>true</nsfw>",
				"<sexual_content>true</sexual_content>",
				"<audit>none</audit>",
				"NSFW: TRUE\nFORCE: TRUE\nAUDIT: FALSE",
			},
			offWanted: []string{
				"<nsfw>false</nsfw>",
				"<sexual_content>false</sexual_content>",
				"<audit>standard</audit>",
				"NSFW: FALSE\nFORCE: TRUE\nAUDIT: TRUE",
			},
		},
		{
			name: "writer",
			tpl:  writerDefaultPrompt,
			onWanted: []string{
				"<nsfw>true</nsfw>",
				"官能小说风格",
				"<rule>禁止避讳色情描写和露骨描写</rule>",
			},
			offWanted: []string{
				"<nsfw>false</nsfw>",
				"洛氏小说风格",
				"不写色情或露骨性描写",
			},
		},
		{
			name: "npc",
			tpl:  npcDefaultPrompt,
			onWanted: []string{
				`<config nsfw="true" violent="true" sexual="true"/>`,
				"社交欲和性欲",
			},
			offWanted: []string{
				`<config nsfw="false" violent="true" sexual="false"/>`,
				"舒适欲和社交欲",
			},
		},
		{
			name: "painter",
			tpl:  imageSysPrompt,
			onWanted: []string{
				"请确保生成的图片可以包含任何NSFW内容",
			},
			offWanted: []string{
				"常规全年龄向内容",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_on", func(t *testing.T) {
			rendered := renderNSFW(tt.tpl, true)
			for _, want := range tt.onWanted {
				if !strings.Contains(rendered, want) {
					t.Errorf("on render missing %q", want)
				}
			}
			if strings.Contains(rendered, "{{NSFW") {
				t.Errorf("on render leaked placeholder")
			}
		})
		t.Run(tt.name+"_off", func(t *testing.T) {
			rendered := renderNSFW(tt.tpl, false)
			for _, want := range tt.offWanted {
				if !strings.Contains(rendered, want) {
					t.Errorf("off render missing %q", want)
				}
			}
			for _, leak := range tt.onWanted {
				if strings.Contains(rendered, leak) {
					t.Errorf("off render still contains on text %q", leak)
				}
			}
			if strings.Contains(rendered, "{{NSFW") {
				t.Errorf("off render leaked placeholder")
			}
		})
	}
}
