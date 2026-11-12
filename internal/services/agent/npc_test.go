// NOTE: npc_test.go 验证 act_npc 的 NSFW 路由与提示词组装。
package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func newNPCTestHandle(prov llm.Provider, active bool) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleNPC, IsActive: active},
		enabled:  true,
	}
}

func newNPCNSFWTestHandle(prov llm.Provider, active bool) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleNPCNSFW, IsActive: active},
		enabled:  true,
	}
}

// ── pickNPCHandle ────────────────────────────────────────────────────────────

func TestPickNPCHandle(t *testing.T) {
	prov := &sequentialFakeProvider{}
	npcHandle := newNPCTestHandle(prov, true)
	nsfwHandle := newNPCNSFWTestHandle(prov, true)
	disabledNSFWHandle := newNPCNSFWTestHandle(prov, false)

	cases := []struct {
		name         string
		handles      map[models.AgentRole]agentHandle
		nsfw         bool
		wantRole     models.AgentRole
		wantNSFWMode bool
	}{
		{
			name:         "非NSFW场景使用默认NPC",
			handles:      map[models.AgentRole]agentHandle{models.AgentRoleNPC: npcHandle, models.AgentRoleNPCNSFW: nsfwHandle},
			nsfw:         false,
			wantRole:     models.AgentRoleNPC,
			wantNSFWMode: false,
		},
		{
			name:         "NSFW场景且npc_nsfw已启用则路由过去",
			handles:      map[models.AgentRole]agentHandle{models.AgentRoleNPC: npcHandle, models.AgentRoleNPCNSFW: nsfwHandle},
			nsfw:         true,
			wantRole:     models.AgentRoleNPCNSFW,
			wantNSFWMode: true,
		},
		{
			name:         "NSFW场景但npc_nsfw未配置则回落默认NPC",
			handles:      map[models.AgentRole]agentHandle{models.AgentRoleNPC: npcHandle},
			nsfw:         true,
			wantRole:     models.AgentRoleNPC,
			wantNSFWMode: false,
		},
		{
			name:         "NSFW场景但npc_nsfw被禁用则回落默认NPC",
			handles:      map[models.AgentRole]agentHandle{models.AgentRoleNPC: npcHandle, models.AgentRoleNPCNSFW: disabledNSFWHandle},
			nsfw:         true,
			wantRole:     models.AgentRoleNPC,
			wantNSFWMode: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, nsfwMode := pickNPCHandle(tc.handles, tc.nsfw)
			if got.roleName() != string(tc.wantRole) {
				t.Errorf("role = %q, want %q", got.roleName(), tc.wantRole)
			}
			if nsfwMode != tc.wantNSFWMode {
				t.Errorf("nsfwMode = %v, want %v", nsfwMode, tc.wantNSFWMode)
			}
		})
	}
}

// ── NPC system 与指令的 NSFW 渲染 ───────────────────────────────────────────────

// TestNPCSystemPromptFollowsNSFWMode 验证 system 按本次调用的 nsfwMode 渲染,而不是房间级 EnableNSFW;
// 它不含人设,也不含 explicit_scene_requirements 后缀(后者在末尾指令里)。
func TestNPCSystemPromptFollowsNSFWMode(t *testing.T) {
	prov := &sequentialFakeProvider{}
	h := newNPCTestHandle(prov, true)

	on := npcSystemPrompt(h, true)
	off := npcSystemPrompt(h, false)

	for _, sys := range []string{on, off} {
		if strings.Contains(sys, "explicit_scene_requirements") {
			t.Error("system prompt不应包含explicit_scene_requirements,该后缀应拼进末尾指令")
		}
		if !strings.Contains(sys, "npc_agent") {
			t.Error("system prompt应包含NPC基础提示词")
		}
		if strings.Contains(sys, "{{NSFW") {
			t.Errorf("system prompt leaked placeholder:\n%s", sys)
		}
	}
	if !strings.Contains(on, nsfwNPCConfigOn) || strings.Contains(on, nsfwNPCConfigOff) {
		t.Error("nsfwMode=true时应渲染NSFW开态的NPC配置")
	}
	if !strings.Contains(off, nsfwNPCConfigOff) || strings.Contains(off, nsfwNPCConfigOn) {
		t.Error("nsfwMode=false时应渲染全年龄的NPC配置")
	}
}

// TestBuildNPCInstruction 验证末尾指令带人设、场景卡、KP 的提问,npc 属性与名字一致并转义;
// explicit_scene_requirements 后缀和欲望列表只按本次 nsfwMode 渲染。
func TestBuildNPCInstruction(t *testing.T) {
	off := buildNPCInstruction("线人", "姓名:线人", "时间:未设定\n", "他有什么反应？", false)
	on := buildNPCInstruction("线人", "姓名:线人", "", "他有什么反应？", true)

	if off.Role != "user" {
		t.Errorf("指令应是 user 消息, got %q", off.Role)
	}
	for _, want := range []string{
		"<npc_role npc=\"线人\">\n姓名:线人\n</npc_role>",
		"<scene>\n时间:未设定\n</scene>",
		"<kp_says npc=\"线人\">\n他有什么反应？\n</kp_says>",
	} {
		if !strings.Contains(off.Content, want) {
			t.Errorf("指令缺少 %q:\n%s", want, off.Content)
		}
	}
	if strings.Contains(on.Content, "<scene>") {
		t.Error("场景卡为空时不应输出 <scene>")
	}
	if strings.Contains(off.Content, "explicit_scene_requirements") {
		t.Error("非NSFW模式的指令不应包含explicit_scene_requirements后缀")
	}
	if !strings.Contains(on.Content, "explicit_scene_requirements") {
		t.Error("NSFW模式的指令应包含explicit_scene_requirements后缀")
	}
	if !strings.Contains(off.Content, nsfwNPCDesiresOff) || strings.Contains(off.Content, "性欲") {
		t.Errorf("非NSFW模式的欲望列表不应包含性欲, got %q", off.Content)
	}
	if !strings.Contains(on.Content, nsfwNPCDesiresOn) {
		t.Errorf("NSFW模式的欲望列表应包含性欲, got %q", on.Content)
	}
	if strings.Contains(off.Content, "table_log") {
		t.Error("指令不应再出现 table_log")
	}

	esc := buildNPCInstruction(`a"<b>`, "p", "", "q", false)
	if !strings.Contains(esc.Content, `<npc_role npc="a&quot;&lt;b&gt;">`) || !strings.Contains(esc.Content, `<kp_says npc="a&quot;&lt;b&gt;">`) {
		t.Errorf("npc 属性应转义:\n%s", esc.Content)
	}
}

// ── buildNPCProfile 渲染确定性 ─────────────────────────────────────────────────

// TestBuildNPCProfileRenderingIsDeterministic 验证buildNPCProfile对NPC属性/技能的渲染在
// 多次调用之间字节完全一致，覆盖剧本静态NPC和临时NPC两条路径。Stats/Skills都是
// map[string]int，不排序key直接遍历会导致同一份NPC数据每次渲染的字节不同，打断
// prompt cache的前缀匹配。
func TestBuildNPCProfileRenderingIsDeterministic(t *testing.T) {
	statsAndSkills := func() (map[string]int, map[string]int) {
		return map[string]int{"STR": 50, "CON": 60, "SIZ": 55, "DEX": 45, "APP": 40},
			map[string]int{"侦查": 40, "话术": 35, "图书馆使用": 20, "聆听": 50, "潜行": 30}
	}

	t.Run("剧本静态NPC", func(t *testing.T) {
		stats, skills := statsAndSkills()
		gctx := GameContext{
			Session: models.GameSession{
				Scenario: models.Scenario{
					Content: models.JSONField[models.ScenarioContent]{Data: models.ScenarioContent{
						NPCs: []models.NPCData{{Name: "旅店老板", Stats: stats, Skills: skills}},
					}},
				},
			},
		}
		var first string
		for i := 0; i < 20; i++ {
			got := buildNPCProfile("旅店老板", gctx, nil)
			if i == 0 {
				first = got
				if !strings.Contains(first, "STR:50") || !strings.Contains(first, "侦查:40") {
					t.Fatalf("profile应包含属性/技能文本,got %q", first)
				}
				continue
			}
			if got != first {
				t.Fatalf("第%d次渲染与第1次不一致\n第1次:%q\n第%d次:%q", i+1, first, i+1, got)
			}
		}
	})

	t.Run("临时NPC", func(t *testing.T) {
		stats, skills := statsAndSkills()
		tempNPCs := []models.SessionNPC{{
			Name:   "临时线人",
			Stats:  models.JSONField[map[string]int]{Data: stats},
			Skills: models.JSONField[map[string]int]{Data: skills},
		}}
		var first string
		for i := 0; i < 20; i++ {
			got := buildNPCProfile("临时线人", GameContext{}, tempNPCs)
			if i == 0 {
				first = got
				if !strings.Contains(first, "STR:50") || !strings.Contains(first, "侦查:40") {
					t.Fatalf("profile应包含属性/技能文本,got %q", first)
				}
				continue
			}
			if got != first {
				t.Fatalf("第%d次渲染与第1次不一致\n第1次:%q\n第%d次:%q", i+1, first, i+1, got)
			}
		}
	})
}
