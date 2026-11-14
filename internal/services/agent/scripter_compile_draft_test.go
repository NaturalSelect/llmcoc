// NOTE: 草稿编辑工具的单元测试：工具定义合法、按键覆盖与删除、写入前的字段校验（被拒绝的调用不改草稿）、
// view_draft 的关卡问题清单、克隆互不影响。不经过工具循环，直接调用 dispatch。禁止真实网络/真实LLM。
package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func callDraftTool(d *compileDraft, name string, args any) toolOutcome {
	raw, ok := args.(string)
	if !ok {
		raw = mustJSON(args)
	}
	return d.dispatch(context.Background(), &scripterRoom{}, llm.ToolCall{ID: "t", Name: name, Arguments: raw})
}

func mustAccept(t *testing.T, out toolOutcome) {
	t.Helper()
	if out.reject != "" {
		t.Fatalf("调用被拒绝: %s", out.reject)
	}
}

func mustReject(t *testing.T, out toolOutcome, want string) {
	t.Helper()
	if out.reject == "" {
		t.Fatalf("调用应被拒绝（期望包含 %q），实际结果: %s", want, out.result)
	}
	if !strings.Contains(out.reject, want) {
		t.Errorf("拒绝原因 = %q，应包含 %q", out.reject, want)
	}
}

// filledCompileDraft 用夹具逐块写出一份完整草稿。
func filledCompileDraft(t *testing.T) *compileDraft {
	t.Helper()
	d := newCompileDraft(StoryOutput{MythosAnchor: compileFixtureDraft.Content.MythosAnchor}, ScripterConstraints{})
	for _, call := range compileWriteCalls(compileFixtureDraft, compileFixtureConcept) {
		mustAccept(t, d.dispatch(context.Background(), &scripterRoom{}, call))
	}
	return d
}

func sceneArg(id string) map[string]any {
	return map[string]any{"id": id, "name": "场景" + id, "description": "描述" + id}
}

func TestCompileDraftToolsDefinitions(t *testing.T) {
	want := []string{
		toolNameSetOverview, toolNameSetKeeperNotes, toolNameUpsertScenes, toolNameUpsertNPCs,
		toolNameUpsertClues, toolNameUpsertEndings, toolNameUpsertMechanics, toolNameSetTimeline,
		toolNameRemoveEntries, toolNameViewDraft, toolNameAskLawyer, toolNameSubmitDraft,
	}
	tools := compileDraftTools()
	got := make(map[string]scripterTool, len(tools))
	for _, tool := range tools {
		if _, dup := got[tool.def.Name]; dup {
			t.Errorf("工具名重复: %s", tool.def.Name)
		}
		got[tool.def.Name] = tool
		if strings.TrimSpace(tool.def.Description) == "" {
			t.Errorf("工具 %s 缺少描述", tool.def.Name)
		}
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.def.Parameters, &schema); err != nil {
			t.Errorf("工具 %s 的参数不是合法 JSON: %v", tool.def.Name, err)
			continue
		}
		if schema.Type != "object" {
			t.Errorf("工具 %s 的参数 type = %q，应为 object", tool.def.Name, schema.Type)
		}
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("缺少工具 %s", name)
		}
	}
	if len(tools) != len(want) {
		t.Errorf("工具数量 = %d, want %d", len(tools), len(want))
	}
	if !got[toolNameSubmitDraft].solo {
		t.Error("submit_draft 必须是 solo 工具，不能和编辑工具同轮调用")
	}
	for name, tool := range got {
		if name != toolNameSubmitDraft && tool.solo {
			t.Errorf("工具 %s 不应是 solo", name)
		}
	}
}

func TestCompileDraftUnknownToolRejected(t *testing.T) {
	d := newCompileDraft(StoryOutput{}, ScripterConstraints{})
	mustReject(t, callDraftTool(d, "set_everything", "{}"), "不允许调用")
}

func TestCompileDraftUpsertOverwritesByKey(t *testing.T) {
	d := newCompileDraft(StoryOutput{}, ScripterConstraints{})
	mustAccept(t, callDraftTool(d, toolNameUpsertScenes, map[string]any{"scenes": []any{sceneArg("a"), sceneArg("b")}}))

	replaced := sceneArg("a")
	replaced["name"] = "改名后的A"
	out := callDraftTool(d, toolNameUpsertScenes, map[string]any{"scenes": []any{replaced, sceneArg("c")}})
	mustAccept(t, out)
	if !strings.Contains(out.result, "覆盖") || !strings.Contains(out.result, "新增") {
		t.Errorf("回执应区分覆盖和新增: %s", out.result)
	}

	var ids []string
	for _, s := range d.draft.Content.Scenes {
		ids = append(ids, s.ID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("场景顺序 = %v，覆盖应原位替换、新条目追加在末尾", ids)
	}
	if d.draft.Content.Scenes[0].Name != "改名后的A" {
		t.Errorf("场景 a 未被覆盖: %q", d.draft.Content.Scenes[0].Name)
	}
	if got := d.draft.Content.Scenes[0].Triggers; !reflect.DeepEqual(got, []string{"available_from_start"}) {
		t.Errorf("未写 triggers 时应默认 available_from_start, got %v", got)
	}
}

func TestCompileDraftUpsertIsAtomic(t *testing.T) {
	d := newCompileDraft(StoryOutput{}, ScripterConstraints{})
	mustAccept(t, callDraftTool(d, toolNameUpsertScenes, map[string]any{"scenes": []any{sceneArg("a")}}))

	bad := map[string]any{"id": "b", "name": "缺描述"}
	mustReject(t, callDraftTool(d, toolNameUpsertScenes, map[string]any{"scenes": []any{sceneArg("c"), bad}}), "scenes[1] 缺少 description")
	if n := len(d.draft.Content.Scenes); n != 1 {
		t.Errorf("有一条不合格时整次调用都不应写入, 场景数 = %d, want 1", n)
	}

	mustReject(t, callDraftTool(d, toolNameUpsertScenes, map[string]any{"scenes": []any{sceneArg("x"), sceneArg("x")}}), "出现了多次")
	if n := len(d.draft.Content.Scenes); n != 1 {
		t.Errorf("同一次调用里键重复应整体拒绝, 场景数 = %d, want 1", n)
	}
}

func TestCompileDraftUnknownFieldRejected(t *testing.T) {
	d := newCompileDraft(StoryOutput{MythosAnchor: "锚点"}, ScripterConstraints{})
	mustReject(t, callDraftTool(d, toolNameSetKeeperNotes, `{"mythos_anchor":"篡改"}`), "参数不合法")
	mustReject(t, callDraftTool(d, toolNameUpsertScenes, `{"scenes":[{"id":"a","name":"n","description":"d","secret":"x"}]}`), "参数不合法")
	mustReject(t, callDraftTool(d, toolNameSetOverview, `not json`), "参数不合法")
}

func TestCompileDraftClueKeysStayParallel(t *testing.T) {
	d := filledCompileDraft(t)
	if !reflect.DeepEqual(d.clueKeys, compileFixtureClueKeys) {
		t.Fatalf("clueKeys = %v, want %v", d.clueKeys, compileFixtureClueKeys)
	}

	mustAccept(t, callDraftTool(d, toolNameRemoveEntries, map[string]any{"section": "clues", "keys": []string{"window_soil"}}))
	if len(d.draft.Content.Clues) != 3 || len(d.clueKeys) != 3 {
		t.Fatalf("删除后线索 %d 条、键 %d 个，应同为 3", len(d.draft.Content.Clues), len(d.clueKeys))
	}
	if d.clueKeys[1] != "ghoul_truth" || d.draft.Content.Clues[1].Nature != "隐藏" {
		t.Errorf("删除后键与线索错位: keys=%v, clues[1].Nature=%q", d.clueKeys, d.draft.Content.Clues[1].Nature)
	}

	cl := compileFixtureDraft.Content.Clues[0]
	cl.Summary = "被改写的线索摘要"
	mustAccept(t, callDraftTool(d, toolNameUpsertClues, map[string]any{"clues": []clueArg{{Key: "donor_books", ClueData: cl}}}))
	if len(d.draft.Content.Clues) != 3 || d.draft.Content.Clues[0].Summary != "被改写的线索摘要" {
		t.Errorf("按键覆盖线索失败: %d 条, clues[0]=%q", len(d.draft.Content.Clues), d.draft.Content.Clues[0].Summary)
	}

	view := callDraftTool(d, toolNameViewDraft, map[string]any{"section": "clues"})
	mustAccept(t, view)
	if !strings.Contains(view.result, `"key":"donor_books"`) || strings.Contains(view.result, "window_soil") {
		t.Errorf("clues 视图应带键且不含已删除的线索: %s", view.result)
	}
}

func TestCompileDraftClueValidation(t *testing.T) {
	d := newCompileDraft(StoryOutput{}, ScripterConstraints{})
	badNature := map[string]any{"clues": []map[string]any{{"key": "k", "summary": "s", "nature": "虚构"}}}
	mustReject(t, callDraftTool(d, toolNameUpsertClues, badNature), "nature 必须是")
	noKey := map[string]any{"clues": []map[string]any{{"summary": "s", "nature": "真实"}}}
	mustReject(t, callDraftTool(d, toolNameUpsertClues, noKey), "缺少 key")
	if len(d.draft.Content.Clues) != 0 || len(d.clueKeys) != 0 {
		t.Errorf("被拒绝的调用不应写入线索: %d 条, %d 个键", len(d.draft.Content.Clues), len(d.clueKeys))
	}
}

func TestCompileDraftMechanicValidation(t *testing.T) {
	d := newCompileDraft(StoryOutput{}, ScripterConstraints{})
	mustReject(t, callDraftTool(d, toolNameUpsertMechanics, map[string]any{"mechanics": []map[string]any{{"name": "腐蚀", "type": "meter"}}}), "type 必须是")
	badStage := map[string]any{"mechanics": []map[string]any{{"name": "腐蚀", "type": "clock", "stages": []map[string]any{{"effect": "无标签"}}}}}
	mustReject(t, callDraftTool(d, toolNameUpsertMechanics, badStage), "缺少 label")
	good := map[string]any{"mechanics": []map[string]any{{"name": "腐蚀", "type": "clock", "stages": []map[string]any{{"label": "一阶段"}}}}}
	mustAccept(t, callDraftTool(d, toolNameUpsertMechanics, good))
	if len(d.draft.Content.Mechanics) != 1 {
		t.Errorf("机制数 = %d, want 1", len(d.draft.Content.Mechanics))
	}
}

func TestCompileDraftRemoveEntries(t *testing.T) {
	d := filledCompileDraft(t)
	mustAccept(t, callDraftTool(d, toolNameUpsertScenes, map[string]any{"scenes": []any{sceneArg("a"), sceneArg("b")}}))
	before := len(d.draft.Content.Scenes)

	out := callDraftTool(d, toolNameRemoveEntries, map[string]any{"section": "scenes", "keys": []string{"a", "ghost"}})
	mustReject(t, out, "ghost")
	if !strings.Contains(out.reject, "library_main") {
		t.Errorf("拒绝时应列出当前有效的键: %s", out.reject)
	}
	if len(d.draft.Content.Scenes) != before {
		t.Errorf("含未知键的删除应整体不生效, 场景数 %d -> %d", before, len(d.draft.Content.Scenes))
	}

	mustAccept(t, callDraftTool(d, toolNameRemoveEntries, map[string]any{"section": "scenes", "keys": []string{"a", "b"}}))
	if len(d.draft.Content.Scenes) != before-2 {
		t.Errorf("场景数 = %d, want %d", len(d.draft.Content.Scenes), before-2)
	}

	mustReject(t, callDraftTool(d, toolNameRemoveEntries, map[string]any{"section": "timeline", "keys": []string{"x"}}), "section 必须是")
	mustReject(t, callDraftTool(d, toolNameRemoveEntries, map[string]any{"section": "scenes", "keys": []string{}}), "至少需要一个")
}

func TestCompileDraftSetOverviewValidation(t *testing.T) {
	d := newCompileDraft(StoryOutput{}, ScripterConstraints{})
	mustAccept(t, callDraftTool(d, toolNameSetOverview, map[string]any{"name": "原名", "game_start_time": "1924-09-03T18:00"}))

	mustReject(t, callDraftTool(d, toolNameSetOverview, map[string]any{"name": "新名", "game_start_time": "1924年9月3日"}), "game_start_time")
	if d.draft.Name != "原名" || d.draft.Content.GameStartTime != "1924-09-03T18:00" {
		t.Errorf("被拒绝的调用不应写入任何字段: name=%q start=%q", d.draft.Name, d.draft.Content.GameStartTime)
	}

	mustReject(t, callDraftTool(d, toolNameSetOverview, "{}"), "没有传入任何字段")

	mustAccept(t, callDraftTool(d, toolNameSetOverview, map[string]any{"name": ""}))
	if d.draft.Name != "" {
		t.Errorf("显式传空串应清空字段, name=%q", d.draft.Name)
	}
	if d.draft.Content.GameStartTime != "1924-09-03T18:00" {
		t.Errorf("未传入的字段不应被动: start=%q", d.draft.Content.GameStartTime)
	}
}

func TestCompileDraftSetOverviewPlayersAndDifficulty(t *testing.T) {
	d := newCompileDraft(StoryOutput{}, ScripterConstraints{})
	mustAccept(t, callDraftTool(d, toolNameSetOverview, map[string]any{"min_players": 2, "max_players": 4, "difficulty": "hard"}))
	if d.draft.MinPlayers != 2 || d.draft.MaxPlayers != 4 || d.draft.Difficulty != "hard" {
		t.Fatalf("写入后 players=%d-%d difficulty=%q", d.draft.MinPlayers, d.draft.MaxPlayers, d.draft.Difficulty)
	}

	for name, args := range map[string]map[string]any{
		"人数低于 1":      {"name": "新名", "min_players": 0},
		"人数超过上限":      {"name": "新名", "max_players": 6},
		"最大小于已写的最小":   {"name": "新名", "max_players": 1},
		"最小大于已写的最大":   {"name": "新名", "min_players": 5},
		"难度不在枚举内":     {"name": "新名", "difficulty": "legendary"},
		"同一调用里最大小于最小": {"name": "新名", "min_players": 3, "max_players": 2},
	} {
		t.Run(name, func(t *testing.T) {
			before := d.draft
			mustReject(t, callDraftTool(d, toolNameSetOverview, args), "")
			if d.draft.Name != before.Name || d.draft.MinPlayers != before.MinPlayers ||
				d.draft.MaxPlayers != before.MaxPlayers || d.draft.Difficulty != before.Difficulty {
				t.Errorf("被拒绝的调用不应写入任何字段: %+v", d.draft)
			}
		})
	}
}

func TestCompileDraftFixedToneTags(t *testing.T) {
	fixed := []string{"cosmic-dread", "occult-noir"}
	d := newCompileDraft(StoryOutput{}, ScripterConstraints{ToneTags: fixed})
	if !reflect.DeepEqual(d.draft.Content.ToneTags, fixed) {
		t.Fatalf("初始 tone_tags = %v, want %v", d.draft.Content.ToneTags, fixed)
	}
	mustReject(t, callDraftTool(d, toolNameSetOverview, map[string]any{"name": "名", "tone_tags": []string{"other"}}), "tone_tags 由系统固定")
	if d.draft.Name != "" {
		t.Errorf("tone_tags 被拒绝时同一调用的其他字段也不应写入, name=%q", d.draft.Name)
	}
	mustAccept(t, callDraftTool(d, toolNameSetOverview, map[string]any{"tone_tags": fixed}))
}

func TestCompileDraftSetKeeperNotes(t *testing.T) {
	d := newCompileDraft(StoryOutput{}, ScripterConstraints{})
	mustReject(t, callDraftTool(d, toolNameSetKeeperNotes, "{}"), "没有传入任何字段")
	if d.draft.Content.KeeperAppendix != nil {
		t.Error("被拒绝的空调用不应留下空的守密人附录")
	}

	mustAccept(t, callDraftTool(d, toolNameSetKeeperNotes, map[string]any{"mythos_core": "神话本质", "core_truth": "核心真相"}))
	mustAccept(t, callDraftTool(d, toolNameSetKeeperNotes, map[string]any{"solo_advice": "单人建议"}))
	c := d.draft.Content
	if c.MythosCore != "神话本质" || c.KeeperAppendix == nil || c.KeeperAppendix.CoreTruth != "核心真相" || c.KeeperAppendix.SoloAdvice != "单人建议" {
		t.Errorf("分次写入的字段应累积: mythos_core=%q appendix=%+v", c.MythosCore, c.KeeperAppendix)
	}
}

func TestCompileDraftSetTimeline(t *testing.T) {
	d := newCompileDraft(StoryOutput{}, ScripterConstraints{})
	quote := map[string]any{"events": []map[string]any{{"time": "三天前", "event": `阿黛莱德首次向神父吐露"醒来身边是凉的"`, "phase": "past"}}}
	mustReject(t, callDraftTool(d, toolNameSetTimeline, quote), "对话引语")

	badPhase := map[string]any{"events": []map[string]any{{"time": "三天前", "event": "教堂完成翻修", "phase": "future"}}}
	mustReject(t, callDraftTool(d, toolNameSetTimeline, badPhase), "phase 必须是")

	mustReject(t, callDraftTool(d, toolNameSetTimeline, "{}"), "缺少 events")
	if len(d.draft.Content.Timeline) != 0 {
		t.Errorf("被拒绝的调用不应写入时间线: %v", d.draft.Content.Timeline)
	}

	good := map[string]any{"events": []map[string]any{{"time": "三天前", "event": "教堂完成翻修", "phase": "past"}, {"time": "开局当晚", "event": "守墓人锁上了偏门"}}}
	mustAccept(t, callDraftTool(d, toolNameSetTimeline, good))
	if len(d.draft.Content.Timeline) != 2 {
		t.Fatalf("时间线条数 = %d, want 2", len(d.draft.Content.Timeline))
	}
	mustAccept(t, callDraftTool(d, toolNameSetTimeline, map[string]any{"events": []any{}}))
	if len(d.draft.Content.Timeline) != 0 {
		t.Errorf("传空数组应清空时间线, got %d 条", len(d.draft.Content.Timeline))
	}
}

func TestCompileDraftViewListsGateIssues(t *testing.T) {
	d := newCompileDraft(StoryOutput{MythosAnchor: "食尸鬼（Ghoul）"}, ScripterConstraints{})
	out := callDraftTool(d, toolNameViewDraft, "{}")
	mustAccept(t, out)
	if !strings.Contains(out.result, "<draft_overview>") || !strings.Contains(out.result, "未填写") {
		t.Errorf("概览应列出各字段填写状态: %s", out.result)
	}
	if !strings.Contains(out.result, `<gate_issues count="`) || strings.Contains(out.result, `count="0"`) {
		t.Errorf("空草稿的概览应带非零数量的关卡问题: %s", out.result)
	}
	if !strings.Contains(out.result, "reward_concept 为空") {
		t.Errorf("关卡问题应包含 reward_concept 为空: %s", out.result)
	}

	full := filledCompileDraft(t)
	out = callDraftTool(full, toolNameViewDraft, "{}")
	mustAccept(t, out)
	if !strings.Contains(out.result, `<gate_issues count="0">`) {
		t.Errorf("夹具草稿应能通过关卡: %s", out.result)
	}
	if issues := full.gateIssues(nil); len(issues) != 0 {
		t.Errorf("夹具草稿 gateIssues = %v, want 空", issues)
	}
}

func TestCompileDraftViewSections(t *testing.T) {
	d := filledCompileDraft(t)
	for _, section := range compileViewSections {
		out := callDraftTool(d, toolNameViewDraft, map[string]any{"section": section})
		mustAccept(t, out)
		open := `<draft_section name="` + section + `">`
		if !strings.HasPrefix(out.result, open) || !strings.HasSuffix(out.result, "</draft_section>") {
			t.Errorf("section=%s 的视图缺少 draft_section 包裹: %s", section, out.result)
			continue
		}
		body := strings.TrimSuffix(strings.TrimPrefix(out.result, open), "</draft_section>")
		if !json.Valid([]byte(body)) {
			t.Errorf("section=%s 的视图正文不是合法 JSON: %s", section, body)
		}
	}
	notes := callDraftTool(d, toolNameViewDraft, map[string]any{"section": "keeper_notes"})
	if !strings.Contains(notes.result, "Douglas") {
		t.Errorf("keeper_notes 视图应带守密人材料正文: %s", notes.result)
	}
	mustReject(t, callDraftTool(d, toolNameViewDraft, map[string]any{"section": "reward"}), "section 必须是")
}

func TestCompileDraftBannedKeywordsGate(t *testing.T) {
	d := filledCompileDraft(t)
	if len(bannedStoryCliches) == 0 {
		t.Skip("没有配置禁用关键词")
	}
	mustAccept(t, callDraftTool(d, toolNameSetOverview, map[string]any{"name": "关于" + bannedStoryCliches[0] + "的事"}))
	issues := d.gateIssues(nil)
	if len(issues) != 1 || !strings.Contains(issues[0], "禁用关键词") {
		t.Errorf("gateIssues = %v，应只有一条禁用关键词问题", issues)
	}
}

func TestCompileDraftCloneIsDeep(t *testing.T) {
	d := filledCompileDraft(t)
	mustAccept(t, callDraftTool(d, toolNameUpsertMechanics, map[string]any{"mechanics": []map[string]any{
		{"name": "腐蚀", "type": "clock", "stages": []map[string]any{{"label": "一阶段"}}},
	}}))
	d.fixedToneTags = []string{"cosmic-dread"}
	d.draft.Content.Reward = &models.ScenarioReward{Name: "旧奖励"}
	snapshot := mustJSON(d.draft) + "|" + d.rewardConcept + "|" + strings.Join(d.clueKeys, ",") + "|" + strings.Join(d.fixedToneTags, ",")

	c := d.clone()
	c.draft.Name = "克隆改名"
	c.rewardConcept = "克隆概念"
	c.clueKeys[0] = "changed"
	c.fixedToneTags[0] = "changed"
	c.draft.Content.ToneTags[0] = "changed"
	c.draft.Content.Scenes[0].Triggers[0] = "changed"
	c.draft.Content.NPCs[0].Stats["STR"] = 1
	c.draft.Content.NPCs[0].Skills["侦查"] = 1
	c.draft.Content.NPCs[0].Spells = append(c.draft.Content.NPCs[0].Spells, "changed")
	c.draft.Content.Clues[0].Summary = "changed"
	c.draft.Content.Endings[0].Name = "changed"
	c.draft.Content.Timeline[0].Event = "changed"
	c.draft.Content.Mechanics[0].Stages[0].Label = "changed"
	c.draft.Content.KeeperAppendix.CoreTruth = "changed"
	c.draft.Content.Reward.Name = "changed"

	after := mustJSON(d.draft) + "|" + d.rewardConcept + "|" + strings.Join(d.clueKeys, ",") + "|" + strings.Join(d.fixedToneTags, ",")
	if after != snapshot {
		t.Errorf("修改克隆影响了原草稿:\nbefore=%s\nafter =%s", snapshot, after)
	}
}
