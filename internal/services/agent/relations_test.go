// NOTE: 测试社交关系分层(tier1 会话 / tier2 人物卡)的合并与视图纯函数。
package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func sr(name, relationship, note string) models.SocialRelation {
	return models.SocialRelation{Name: name, Relationship: relationship, Note: note}
}

// ── upsertSessionRelation ──────────────────────────────────────────────────

func TestUpsertSessionRelation_Add(t *testing.T) {
	list := upsertSessionRelation(nil, sr("张三", "线人", "警局线人"), false)
	want := []models.SessionRelation{{SocialRelation: sr("张三", "线人", "警局线人")}}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("got %+v, want %+v", list, want)
	}
}

func TestUpsertSessionRelation_OverwriteSameName(t *testing.T) {
	list := []models.SessionRelation{{SocialRelation: sr("张三", "线人", "旧备注")}}
	list = upsertSessionRelation(list, sr("张三", "盟友", "新备注"), false)
	if len(list) != 1 {
		t.Fatalf("want 1 entry, got %d", len(list))
	}
	if list[0].Relationship != "盟友" || list[0].Note != "新备注" || list[0].Removed {
		t.Errorf("got %+v", list[0])
	}
}

func TestUpsertSessionRelation_RemoveWritesTombstone(t *testing.T) {
	list := []models.SessionRelation{{SocialRelation: sr("张三", "线人", "备注")}}
	list = upsertSessionRelation(list, sr("张三", "线人", "备注"), true)
	if len(list) != 1 || !list[0].Removed {
		t.Fatalf("want tombstoned single entry, got %+v", list)
	}
}

func TestUpsertSessionRelation_ReviveAfterTombstone(t *testing.T) {
	list := []models.SessionRelation{{SocialRelation: sr("张三", "线人", "备注"), Removed: true}}
	list = upsertSessionRelation(list, sr("张三", "盟友", "复活"), false)
	if len(list) != 1 || list[0].Removed {
		t.Fatalf("want revived entry, got %+v", list)
	}
	if list[0].Relationship != "盟友" {
		t.Errorf("relationship = %q, want 盟友", list[0].Relationship)
	}
}

// ── effectiveRelations ──────────────────────────────────────────────────────

func TestEffectiveRelations_TierMerging(t *testing.T) {
	card := []models.SocialRelation{sr("张三", "线人", "旧"), sr("李四", "仇敌", "")}
	sess := []models.SessionRelation{
		{SocialRelation: sr("张三", "盟友", "新")}, // 更新已有
		{SocialRelation: sr("李四", "", ""), Removed: true}, // 移除已有
		{SocialRelation: sr("王五", "新朋友", "本局认识")}, // 新增
	}
	got := effectiveRelations(card, sess)
	want := []models.SocialRelation{sr("张三", "盟友", "新"), sr("王五", "新朋友", "本局认识")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestEffectiveRelations_NewTombstoneHidden(t *testing.T) {
	card := []models.SocialRelation{sr("张三", "线人", "")}
	sess := []models.SessionRelation{
		{SocialRelation: sr("王五", "路人", ""), Removed: true}, // 本局加了又删,不应出现
	}
	got := effectiveRelations(card, sess)
	want := []models.SocialRelation{sr("张三", "线人", "")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// ── sessionRelationCandidates ────────────────────────────────────────────────

func TestSessionRelationCandidates(t *testing.T) {
	card := []models.SocialRelation{sr("张三", "线人", "")}
	sess := []models.SessionRelation{
		{SocialRelation: sr("张三", "盟友", "更新")},                  // 已在人物卡,不是候选
		{SocialRelation: sr("李四", "仇敌", "")},                    // 新名字,是候选
		{SocialRelation: sr("王五", "路人", ""), Removed: true},     // 新名字但被墓碑,不是候选
	}
	got := sessionRelationCandidates(card, sess)
	want := []models.SocialRelation{sr("李四", "仇敌", "")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// ── mergeSessionRelations ────────────────────────────────────────────────────

func TestMergeSessionRelations_ExistingUpdateAppliesWithoutAI(t *testing.T) {
	card := []models.SocialRelation{sr("张三", "线人", "旧")}
	sess := []models.SessionRelation{{SocialRelation: sr("张三", "盟友", "新")}}
	got := mergeSessionRelations(card, sess, nil)
	want := []models.SocialRelation{sr("张三", "盟友", "新")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMergeSessionRelations_ExistingTombstoneDeletes(t *testing.T) {
	card := []models.SocialRelation{sr("张三", "线人", ""), sr("李四", "仇敌", "")}
	sess := []models.SessionRelation{{SocialRelation: sr("张三", "", ""), Removed: true}}
	got := mergeSessionRelations(card, sess, nil)
	want := []models.SocialRelation{sr("李四", "仇敌", "")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMergeSessionRelations_NewEntrySelectedByAI(t *testing.T) {
	sess := []models.SessionRelation{{SocialRelation: sr("王五", "新朋友", "本局认识")}}
	promoted := map[string]relationPromotion{"王五": {Name: "王五", Reason: "结下重大人情"}}
	got := mergeSessionRelations(nil, sess, promoted)
	want := []models.SocialRelation{sr("王五", "新朋友", "本局认识")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMergeSessionRelations_NewEntryNotSelectedIsDropped(t *testing.T) {
	sess := []models.SessionRelation{{SocialRelation: sr("路人甲", "问询", "买了张地图")}}
	got := mergeSessionRelations(nil, sess, nil)
	if len(got) != 0 {
		t.Errorf("got %+v, want empty", got)
	}
}

func TestMergeSessionRelations_NewEntryTombstoneIgnored(t *testing.T) {
	sess := []models.SessionRelation{{SocialRelation: sr("王五", "新朋友", ""), Removed: true}}
	promoted := map[string]relationPromotion{"王五": {Name: "王五"}}
	got := mergeSessionRelations(nil, sess, promoted)
	if len(got) != 0 {
		t.Errorf("got %+v, want empty (tombstoned candidate must never be promoted)", got)
	}
}

func TestMergeSessionRelations_PromotionNoteOverridesOriginal(t *testing.T) {
	sess := []models.SessionRelation{{SocialRelation: sr("王五", "新朋友", "本局认识")}}
	promoted := map[string]relationPromotion{"王五": {Name: "王五", Note: "AI精简后的长期备注"}}
	got := mergeSessionRelations(nil, sess, promoted)
	want := []models.SocialRelation{sr("王五", "新朋友", "AI精简后的长期备注")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// ── filterValidPromotions：AI 编造/写错名字的条目应被丢弃 ─────────────────────

func TestFilterValidPromotions_DiscardsNamesNotInCandidates(t *testing.T) {
	llmOut := relationPromotionLLMOutput{Characters: []struct {
		CharacterName string `json:"character_name"`
		Promote       []struct {
			Name   string `json:"name"`
			Note   string `json:"note"`
			Reason string `json:"reason"`
		} `json:"promote"`
	}{
		{
			CharacterName: "张三",
			Promote: []struct {
				Name   string `json:"name"`
				Note   string `json:"note"`
				Reason string `json:"reason"`
			}{
				{Name: "李四", Reason: "确实是候选"},
				{Name: "编造的名字", Reason: "不在候选列表里"},
			},
		},
	}}
	candByChar := map[string]map[string]bool{"张三": {"李四": true}}

	got := filterValidPromotions(llmOut, candByChar)
	if len(got["张三"]) != 1 {
		t.Fatalf("want 1 valid promotion, got %+v", got)
	}
	if _, ok := got["张三"]["李四"]; !ok {
		t.Errorf("want 李四 to survive filtering, got %+v", got["张三"])
	}
	if _, ok := got["张三"]["编造的名字"]; ok {
		t.Errorf("want 编造的名字 discarded, got %+v", got["张三"])
	}
}

func TestFilterValidPromotions_UnknownCharacterIgnored(t *testing.T) {
	llmOut := relationPromotionLLMOutput{Characters: []struct {
		CharacterName string `json:"character_name"`
		Promote       []struct {
			Name   string `json:"name"`
			Note   string `json:"note"`
			Reason string `json:"reason"`
		} `json:"promote"`
	}{
		{CharacterName: "不存在的角色", Promote: []struct {
			Name   string `json:"name"`
			Note   string `json:"note"`
			Reason string `json:"reason"`
		}{{Name: "李四"}}},
	}}
	got := filterValidPromotions(llmOut, map[string]map[string]bool{"张三": {"李四": true}})
	if len(got) != 0 {
		t.Errorf("got %+v, want empty", got)
	}
}

// ── manageSocialRelation：写 tier1，人物卡(tier2)不受影响 ─────────────────────

func TestManageSocialRelation_WritesTier1OnlyOnAdd(t *testing.T) {
	initAgentTestDB(t)
	card := newTestCard(t, "调查员甲", 30)
	players := []models.SessionPlayer{{CharacterCardID: card.ID, CharacterCard: *card}}
	if err := models.DB.Create(&players[0]).Error; err != nil {
		t.Fatalf("create session player: %v", err)
	}

	rel := &models.SocialRelation{Name: "王五", Relationship: "线人", Note: "警局眼线"}
	result := manageSocialRelation(players, "调查员甲", "add", rel)
	if !strings.Contains(result, "本局关系已记录") {
		t.Errorf("result = %q, want mention of 本局关系已记录", result)
	}

	var reloadedCard models.CharacterCard
	if err := models.DB.First(&reloadedCard, card.ID).Error; err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if len(reloadedCard.SocialRelations.Data) != 0 {
		t.Errorf("card social_relations should stay empty, got %+v", reloadedCard.SocialRelations.Data)
	}

	var reloadedPlayer models.SessionPlayer
	if err := models.DB.First(&reloadedPlayer, players[0].ID).Error; err != nil {
		t.Fatalf("reload player: %v", err)
	}
	if len(reloadedPlayer.SessionRelations.Data) != 1 || reloadedPlayer.SessionRelations.Data[0].Name != "王五" {
		t.Errorf("player session_relations = %+v, want one entry for 王五", reloadedPlayer.SessionRelations.Data)
	}
}

func TestManageSocialRelation_RemoveWritesTombstoneOnlyOnTier1(t *testing.T) {
	initAgentTestDB(t)
	card := newTestCard(t, "调查员乙", 30)
	card.SocialRelations.Data = []models.SocialRelation{sr("赵六", "旧友", "")}
	if err := models.DB.Save(card).Error; err != nil {
		t.Fatalf("save card: %v", err)
	}
	players := []models.SessionPlayer{{CharacterCardID: card.ID, CharacterCard: *card}}
	if err := models.DB.Create(&players[0]).Error; err != nil {
		t.Fatalf("create session player: %v", err)
	}

	result := manageSocialRelation(players, "调查员乙", "remove", &models.SocialRelation{Name: "赵六"})
	if !strings.Contains(result, "本局关系已移除") {
		t.Errorf("result = %q, want mention of 本局关系已移除", result)
	}

	var reloadedCard models.CharacterCard
	if err := models.DB.First(&reloadedCard, card.ID).Error; err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if len(reloadedCard.SocialRelations.Data) != 1 {
		t.Errorf("card social_relations should be untouched until settlement, got %+v", reloadedCard.SocialRelations.Data)
	}

	var reloadedPlayer models.SessionPlayer
	if err := models.DB.First(&reloadedPlayer, players[0].ID).Error; err != nil {
		t.Fatalf("reload player: %v", err)
	}
	if len(reloadedPlayer.SessionRelations.Data) != 1 || !reloadedPlayer.SessionRelations.Data[0].Removed {
		t.Errorf("player session_relations = %+v, want tombstoned 赵六", reloadedPlayer.SessionRelations.Data)
	}
}

// ── buildCharacterDetail：<rels> 展示 tier2 叠加 tier1 之后的实时视图 ──────────

func TestBuildCharacterDetail_RelsShowsEffectiveView(t *testing.T) {
	initAgentTestDB(t)
	card := newTestCard(t, "调查员丙", 30)
	card.SocialRelations.Data = []models.SocialRelation{sr("赵六", "旧友", ""), sr("孙七", "仇敌", "")}
	if err := models.DB.Save(card).Error; err != nil {
		t.Fatalf("save card: %v", err)
	}
	players := []models.SessionPlayer{{
		CharacterCard: *card,
		SessionRelations: models.JSONField[[]models.SessionRelation]{Data: []models.SessionRelation{
			{SocialRelation: sr("孙七", "", ""), Removed: true},        // 本局移除
			{SocialRelation: sr("王五", "新朋友", "本局刚认识")}, // 本局新增
		}},
	}}

	out := buildCharacterDetail("调查员丙", players)
	if !strings.Contains(out, `n="王五"`) {
		t.Errorf("expected 王五 (tier1新增) in output, got %s", out)
	}
	if !strings.Contains(out, `n="赵六"`) {
		t.Errorf("expected 赵六 (tier2未变) in output, got %s", out)
	}
	if strings.Contains(out, `n="孙七"`) {
		t.Errorf("孙七 was removed this session, should not appear in output: %s", out)
	}
}
