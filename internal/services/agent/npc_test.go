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

// ── buildNPCMessages NSFW后缀 ─────────────────────────────────────────────────

func TestBuildNPCMessagesNSFWSuffix(t *testing.T) {
	prov := &sequentialFakeProvider{}
	h := newNPCTestHandle(prov, true)
	gctx := GameContext{Session: models.GameSession{ID: 1, EnableNSFW: true}}

	msgsOff := buildNPCMessages(h, gctx, "姓名:线人", nil, "他有什么反应？", false)
	msgsOn := buildNPCMessages(h, gctx, "姓名:线人", nil, "他有什么反应？", true)

	sysOff := msgsOff[0].Content
	sysOn := msgsOn[0].Content

	if strings.Contains(sysOff, "explicit_scene_requirements") {
		t.Error("非NSFW模式不应包含explicit_scene_requirements后缀")
	}
	if !strings.Contains(sysOn, "explicit_scene_requirements") {
		t.Error("NSFW模式应包含explicit_scene_requirements后缀")
	}
	if !strings.Contains(sysOff, "npc_agent") || !strings.Contains(sysOn, "npc_agent") {
		t.Error("两种模式都应包含NPC基础提示词,后缀只是增量")
	}
}
