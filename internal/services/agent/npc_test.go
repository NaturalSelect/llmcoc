// NOTE: npc_test.go 验证 act_npc 的 NSFW 路由与提示词组装。
package agent

import (
	"context"
	"reflect"
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

// ── buildNPCHead/buildNPCOpening NSFW后缀 ──────────────────────────────────────

// TestBuildNPCHeadStableAcrossNSFWMode 验证head(system prompt+人设)不再随每轮call的
// nsfwMode变化——只有本次调用的nsfwMode才决定是否追加explicit_scene_requirements,
// 且该后缀现在挪进了opening,保证head跨NSFW/非NSFW调用保持字节稳定。
func TestBuildNPCHeadStableAcrossNSFWMode(t *testing.T) {
	prov := &sequentialFakeProvider{}
	h := newNPCTestHandle(prov, true)
	gctx := GameContext{Session: models.GameSession{ID: 1, EnableNSFW: true}}

	head := buildNPCHead(h, gctx, "姓名:线人")
	sysContent := head[0].Content

	if strings.Contains(sysContent, "explicit_scene_requirements") {
		t.Error("system prompt不应再包含explicit_scene_requirements,该后缀现在应拼进本轮opening")
	}
	if !strings.Contains(sysContent, "npc_agent") {
		t.Error("system prompt应包含NPC基础提示词")
	}
}

// TestBuildNPCOpeningNSFWSuffix 验证explicit_scene_requirements后缀现在拼在本轮
// opening(user消息)尾部,而不是system prompt里。
func TestBuildNPCOpeningNSFWSuffix(t *testing.T) {
	gctx := GameContext{Session: models.GameSession{ID: 1, EnableNSFW: true}}

	openingOff := buildNPCOpening(gctx, "他有什么反应？", false)
	openingOn := buildNPCOpening(gctx, "他有什么反应？", true)

	if strings.Contains(openingOff.Content, "explicit_scene_requirements") {
		t.Error("非NSFW模式的opening不应包含explicit_scene_requirements后缀")
	}
	if !strings.Contains(openingOn.Content, "explicit_scene_requirements") {
		t.Error("NSFW模式的opening应包含explicit_scene_requirements后缀")
	}
}

// TestRunNPC_ContextPrefixStableAcrossTurns 验证同一NPC连续两次act_npc:第2次发给LLM的
// msgs应以"第1次发送的msgs+其assistant响应(LLM原始JSON,不是改写过的摘要)"为逐字段
// 相同的前缀,且CacheBreakpoint只打在head末尾(下标1,人设消息)与上一轮末尾。
func TestRunNPC_ContextPrefixStableAcrossTurns(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 501
	if err := models.DB.Create(&models.GameSession{ID: sessionID}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	prov := &sequentialFakeProvider{jsonResponses: []string{
		`{"action":"皱眉","dialogue":"你想干什么？"}`,
		`{"action":"后退一步","dialogue":"别过来！"}`,
	}}
	h := newNPCTestHandle(prov, true)
	tempNPCs := []models.SessionNPC{{Name: "线人", Description: "一个线人", IsAlive: true}}
	gctx := GameContext{Session: models.GameSession{ID: sessionID}}

	if _, err := runNPC(context.Background(), h, gctx, "线人", "调查员上前搭话。", tempNPCs, false); err != nil {
		t.Fatalf("runNPC turn1: %v", err)
	}
	if _, err := runNPC(context.Background(), h, gctx, "线人", "调查员继续追问。", tempNPCs, false); err != nil {
		t.Fatalf("runNPC turn2: %v", err)
	}

	turn1Msgs := prov.recordedMessages[0]
	turn2Msgs := prov.recordedMessages[1]

	wantAssistant := llm.ChatMessage{Role: "assistant", Content: `{"action":"皱眉","dialogue":"你想干什么？"}`}
	wantPrefix := append(append([]llm.ChatMessage(nil), turn1Msgs...), wantAssistant)
	if len(turn2Msgs) < len(wantPrefix) {
		t.Fatalf("turn2Msgs长度%d应不小于期望前缀长度%d", len(turn2Msgs), len(wantPrefix))
	}
	for i, want := range wantPrefix {
		got := turn2Msgs[i]
		got.CacheBreakpoint = false
		want.CacheBreakpoint = false
		if !reflect.DeepEqual(got, want) {
			t.Errorf("turn2Msgs[%d]与期望前缀不一致:\ngot  %+v\nwant %+v", i, got, want)
		}
	}

	// head长度固定为2(system+人设);turn1归档的历史长度为2(opening+assistant),
	// 所以CacheBreakpoint应恰好落在下标1(head末尾)和下标3(上一轮末尾)。
	for i, m := range turn2Msgs {
		want := i == 1 || i == 3
		if m.CacheBreakpoint != want {
			t.Errorf("turn2Msgs[%d].CacheBreakpoint=%v, want %v", i, m.CacheBreakpoint, want)
		}
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
