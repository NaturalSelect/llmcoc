package agent

import (
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func newChoreTestHandle(prov llm.Provider) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleChore, IsActive: true},
		enabled:  true,
	}
}

func TestChoreHandle(t *testing.T) {
	prov := &nameChatProvider{names: []string{"x"}}
	writer := newWriterTestHandle(prov)
	chore := newChoreTestHandle(prov)
	disabledChore := agentHandle{config: &models.AgentConfig{Role: models.AgentRoleChore}, enabled: false}

	tests := []struct {
		name     string
		handles  map[models.AgentRole]agentHandle
		wantRole models.AgentRole
		wantNone bool
	}{
		{"Chore 启用时优先用 Chore", map[models.AgentRole]agentHandle{models.AgentRoleWriter: writer, models.AgentRoleChore: chore}, models.AgentRoleChore, false},
		{"Chore 未启用时回落 Writer", map[models.AgentRole]agentHandle{models.AgentRoleWriter: writer, models.AgentRoleChore: disabledChore}, models.AgentRoleWriter, false},
		{"没有 Chore 时回落 Writer", map[models.AgentRole]agentHandle{models.AgentRoleWriter: writer}, models.AgentRoleWriter, false},
		{"两者都没有时得到未启用的零值", map[models.AgentRole]agentHandle{}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := choreHandle(tt.handles)
			if tt.wantNone {
				if got.isEnabled() {
					t.Fatalf("handle enabled, want zero handle")
				}
				return
			}
			if !got.isEnabled() || got.config.Role != tt.wantRole {
				t.Fatalf("handle role = %v, want %v", got.config, tt.wantRole)
			}
		})
	}
}

func TestLoadChoreAgent(t *testing.T) {
	// NOTE: IsActive 带 default:true，Create 会把 false 换成 true，所以未启用的行要建完再回写。
	create := func(t *testing.T, role models.AgentRole, provID *uint, active bool) {
		t.Helper()
		cfg := models.AgentConfig{Role: role, ProviderConfigID: provID, ModelName: "m", IsActive: true}
		if err := models.DB.Create(&cfg).Error; err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
		if !active {
			if err := models.DB.Model(&cfg).Update("is_active", false).Error; err != nil {
				t.Fatalf("disable %s: %v", role, err)
			}
		}
	}

	tests := []struct {
		name     string
		setup    func(t *testing.T, provID *uint)
		wantRole models.AgentRole
		wantErr  bool
	}{
		{"Chore 启用且绑定 provider", func(t *testing.T, p *uint) {
			create(t, models.AgentRoleWriter, p, true)
			create(t, models.AgentRoleChore, p, true)
		}, models.AgentRoleChore, false},
		{"Chore 未启用时回落 Writer", func(t *testing.T, p *uint) {
			create(t, models.AgentRoleWriter, p, true)
			create(t, models.AgentRoleChore, p, false)
		}, models.AgentRoleWriter, false},
		{"Chore 未绑定 provider 时回落 Writer", func(t *testing.T, p *uint) {
			create(t, models.AgentRoleWriter, p, true)
			create(t, models.AgentRoleChore, nil, true)
		}, models.AgentRoleWriter, false},
		{"没有 Chore 行时回落 Writer", func(t *testing.T, p *uint) {
			create(t, models.AgentRoleWriter, p, true)
		}, models.AgentRoleWriter, false},
		{"Chore 可用而 Writer 不可用时仍用 Chore", func(t *testing.T, p *uint) {
			create(t, models.AgentRoleWriter, p, false)
			create(t, models.AgentRoleChore, p, true)
		}, models.AgentRoleChore, false},
		{"两者都不可用时报错", func(t *testing.T, p *uint) {
			create(t, models.AgentRoleWriter, p, false)
			create(t, models.AgentRoleChore, p, false)
		}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initSiteSettingTestDB(t)
			if err := models.DB.AutoMigrate(&models.LLMProviderConfig{}, &models.AgentConfig{}); err != nil {
				t.Fatalf("auto-migrate: %v", err)
			}
			prov := models.LLMProviderConfig{Name: "p", Provider: "custom", BaseURL: "http://127.0.0.1:1", IsActive: true}
			if err := models.DB.Create(&prov).Error; err != nil {
				t.Fatalf("create provider: %v", err)
			}
			tt.setup(t, &prov.ID)

			got, err := loadChoreAgent()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("loadChoreAgent() = %v, want error", got.config)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadChoreAgent: %v", err)
			}
			if got.config.Role != tt.wantRole {
				t.Fatalf("role = %q, want %q", got.config.Role, tt.wantRole)
			}
		})
	}
}

// TestGenerateNPCNameActionPrefersChore 验证两个角色都可用时，中文取名走 Chore 的模型而不是 Writer 的。
func TestGenerateNPCNameActionPrefersChore(t *testing.T) {
	writerProv := &nameChatProvider{names: []string{"写手甲"}}
	choreProv := &nameChatProvider{names: []string{"陈守拙", "苏闻笛", "韩知远"}}
	var npcs []models.SessionNPC
	actx := ActionContext{
		GCtx:     &GameContext{},
		TempNPCs: &npcs,
		Handles: map[models.AgentRole]agentHandle{
			models.AgentRoleWriter: newWriterTestHandle(writerProv),
			models.AgentRoleChore:  newChoreTestHandle(choreProv),
		},
	}

	generateNPCNameAction{}.Execute(ToolCall{Action: ToolGenerateNPCName, Culture: "chinese", Gender: "male", Count: 1}, actx)

	if choreProv.calls == 0 {
		t.Fatal("中文取名应调用 Chore 的 provider")
	}
	if writerProv.calls != 0 {
		t.Fatalf("Writer 的 provider 被调用了 %d 次，想要 0 次", writerProv.calls)
	}
}

func batchLoadTestDB(t *testing.T, roles ...models.AgentRole) {
	t.Helper()
	initSiteSettingTestDB(t)
	if err := models.DB.AutoMigrate(&models.LLMProviderConfig{}, &models.AgentConfig{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	prov := models.LLMProviderConfig{Name: "p", Provider: "custom", BaseURL: "http://127.0.0.1:1", IsActive: true}
	if err := models.DB.Create(&prov).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	for _, role := range roles {
		cfg := models.AgentConfig{Role: role, ProviderConfigID: &prov.ID, ModelName: "m", IsActive: true}
		if err := models.DB.Create(&cfg).Error; err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
	}
}

// TestBatchLoadAgentsLoadsEveryRole 守住批量加载结果里有 Director、Lawyer、Writer 和 Chore：
// 漏掉 Lawyer 时 check_rule 等规则查询拿到的是空句柄。
func TestBatchLoadAgentsLoadsEveryRole(t *testing.T) {
	want := []models.AgentRole{models.AgentRoleDirector, models.AgentRoleLawyer, models.AgentRoleWriter, models.AgentRoleChore}
	batchLoadTestDB(t, want...)

	handles, err := batchLoadAgents()
	if err != nil {
		t.Fatalf("batchLoadAgents: %v", err)
	}
	for _, role := range want {
		if h, ok := handles[role]; !ok || !h.isEnabled() {
			t.Errorf("handles[%s] enabled = %v (present %v), want enabled", role, h.isEnabled(), ok)
		}
	}
}

// TestBatchLoadAgentsRequiresLawyer 验证缺少 Lawyer 配置时整体加载失败，而不是悄悄返回没有 Lawyer 的结果。
func TestBatchLoadAgentsRequiresLawyer(t *testing.T) {
	batchLoadTestDB(t, models.AgentRoleDirector, models.AgentRoleWriter)

	if _, err := batchLoadAgents(); err == nil {
		t.Fatal("batchLoadAgents() err = nil, want error when lawyer is not configured")
	}
}
