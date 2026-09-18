// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

// providenceSystemPrompt 是天意(Providence)顾问的系统提示词。
// 天意只负责比对模组大纲、时间线、线索进度与当前对话历史，给出简短的节奏/走向建议，
// 不裁定规则、不掷骰、不操作任何游戏状态、不调用任何工具——纯只读咨询，供Director参考。
const providenceSystemPrompt = `你是COC跑团的戏剧节奏顾问"天意"。你不是守秘人(KP)本人，不裁定规则、不掷骰、不修改任何游戏状态，只负责站在上帝视角比对模组大纲、场景触发条件、时间线、量化机制、线索树与KP专属真相，判断当前对话已经推进到剧情的哪个阶段(导入/调查/启示/高潮/余波)，并给出接下来该发生什么来维持节奏。
用1-3句简短的中文陈述句直接给出结论：当前处于什么阶段、接下来应该发生什么以推进节奏、有没有该出现但还未出现的线索或伏笔。不要输出标签、不要分点列表、不要复述规则、不要自我介绍，你的话会被直接交给KP作为本轮的节奏判断依据。`

// runProvidence 是一次性、只读的剧情节奏顾问调用，不涉及任何工具调用。
// h 未启用(未配置provider/model)时直接返回空字符串；调用失败时记录日志后同样返回空字符串，
// 调用方(Orchestrator)据此优雅跳过，不影响Director主流程。
func runProvidence(ctx context.Context, h agentHandle, gctx GameContext) string {
	if !h.isEnabled() {
		return ""
	}
	msgs := buildProvidenceMessages(h, gctx)
	resp, err := h.provider.Chat(ctx, h.cacheKey(sessionIDFromContextValue(ctx)), msgs)
	if err != nil {
		alog.Error("providence failed", "err", err)
		return ""
	}
	guidance := strings.TrimSpace(stripThinkingBlock(resp))
	debugf("Providence", "guidance=%s", guidance)
	return guidance
}

// buildProvidenceMessages 组装天意顾问的输入。与Director的buildKPMessages各自独立拼接：
// buildKPMessages的XML拼接逻辑与Director自身的工具协议、批次规则、战斗/追逐状态强耦合在一个
// 300余行的函数里，抽出公共函数改动面大、风险高于收益，这里按天意实际需要的字段单独精简拼接一份。
func buildProvidenceMessages(h agentHandle, gctx GameContext) []llm.ChatMessage {
	content := gctx.Session.Scenario.Content.Data

	var sb strings.Builder
	sb.WriteString(formatHistoryTranscript(gctx.History))

	sb.WriteString("\n<current_turn>\n")
	if len(gctx.PendingActions) > 0 {
		for _, a := range gctx.PendingActions {
			sb.WriteString(a.PlayerName + ": " + a.Content + "\n")
		}
	} else {
		sb.WriteString(gctx.UserName + ": " + gctx.UserInput + "\n")
	}
	sb.WriteString("</current_turn>\n")

	if strings.TrimSpace(content.PlaythroughOutline) != "" {
		sb.WriteString("\n<playthrough_outline>\n" + content.PlaythroughOutline + "\n</playthrough_outline>\n")
	}
	if len(content.Scenes) > 0 {
		sb.WriteString("\n<scene_list>\n")
		for _, scene := range content.Scenes {
			trig := ""
			if len(scene.Triggers) > 0 {
				trig = fmt.Sprintf(" 触发条件: %v", scene.Triggers)
			}
			sb.WriteString(fmt.Sprintf("<scene><name>%s</name><description>%s</description><triggers>%s</triggers></scene>\n", scene.Name, scene.Description, trig))
		}
		sb.WriteString("</scene_list>\n")
	}
	if len(content.Timeline) > 0 {
		sb.WriteString("\n<timeline>\n")
		for _, ev := range content.Timeline {
			tag := ""
			switch ev.Phase {
			case "past":
				tag = "[过去] "
			case "current":
				tag = "[当天] "
			}
			sb.WriteString(fmt.Sprintf("  • %s%s：%s\n", tag, ev.Time, ev.Event))
		}
		sb.WriteString("</timeline>\n")
	}
	if len(content.Mechanics) > 0 {
		sb.WriteString("\n<mechanics>\n")
		for _, m := range content.Mechanics {
			sb.WriteString(fmt.Sprintf("  • %s（%s）：%s\n", m.Name, m.Type, m.Description))
			for _, st := range m.Stages {
				line := "      - " + st.Label
				if strings.TrimSpace(st.Trigger) != "" {
					line += "｜触发：" + st.Trigger
				}
				if strings.TrimSpace(st.Effect) != "" {
					line += "｜效果：" + st.Effect
				}
				sb.WriteString(line + "\n")
			}
		}
		sb.WriteString("</mechanics>\n")
	}
	if len(content.Clues) > 0 {
		sb.WriteString("\n<clues>\n")
		for i, c := range content.Clues {
			sb.WriteString(fmt.Sprintf("[Idx: %d] [%s] %s", i, c.Nature, c.Summary))
			if strings.TrimSpace(c.Source) != "" {
				sb.WriteString("（来源：" + c.Source + "）")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("</clues>\n")
	}
	if ka := content.KeeperAppendix; ka != nil {
		if strings.TrimSpace(ka.CoreTruth) != "" {
			sb.WriteString("\n<core_truth>\n" + ka.CoreTruth + "\n</core_truth>\n")
		}
		if strings.TrimSpace(ka.AntagonistDossier) != "" {
			sb.WriteString("\n<antagonist_dossier>\n" + ka.AntagonistDossier + "\n</antagonist_dossier>\n")
		}
	}
	sb.WriteString("\n<now>当前时间(每轮=游戏内30分钟): " + formatGameTime(gctx.Session.TurnRound, scenarioStartSlot(gctx.Session)) + "</now>\n")
	sb.WriteString("\n<instruction>结合以上信息，判断当前应视为剧情的哪个阶段、接下来应该发生什么来推进节奏、有没有该出现但还未出现的线索或伏笔，用1-3句简短陈述给出结论。</instruction>\n")

	return []llm.ChatMessage{
		{Role: "system", Content: h.systemPrompt(providenceSystemPrompt)},
		{Role: "user", Content: sb.String()},
	}
}
