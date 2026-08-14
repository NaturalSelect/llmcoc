// scripter_title.go — Title agent: 为已定稿的模组拟定标题。
//
// 标题原本由 story architect 在写数千字故事文档时顺手写下一行，之后穿过 story 修复、
// QA、逻辑审查、compile、结构修复、逻辑修复共 6 个环节，没有任何一环检查它，机制层
// 也只有非空校验。结果是标题经常退化成主谓宾齐全的事件陈述句（"某某农场赔了三栏羊"），
// 读起来像事故记录而不是模组名。
//
// 这里把取标题拆成一次上下文干净的独立调用：只送定稿后的核心信息，模型全部注意力在
// 标题上，产出再过一遍确定性判据驱动重写。判据用尽后一律接受最后一个候选——标题问题
// 绝不允许中断一条已经跑了几十分钟的生成管线。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

// scenarioTitleRules 是标题规则的唯一出处，由 story 阶段与本环节共用，避免两处措辞漂移。
//
// 正例只给结构范式、不给成品标题：仓库既有经验表明，给模型完整示例会让它模仿示例用词，
// 造成跨剧本同质（见 compilerSchemaTemplate 上方注释）。
const scenarioTitleRules = `- 形式：标题是一个名词性短语，不是一句话。可以是"专名+具体名词"，可以是带修饰语的偏正结构，也可以是一个单独的专名、器物名或典籍名。禁止主谓宾齐全的陈述句、口语句、带"了/着"的完成态叙述、疑问句与感叹句——那读起来像事故记录或账本条目，不像模组名
- 长度：4到12个汉字。标题内不出现句号、逗号、分号、顿号、问号、感叹号；不加书名号
- 取材：从本剧本已确立的地名、机构名、器物名、典籍名、日期节令、职业称谓中取一到两个词，与一个具体名词组合。标题指向的东西必须在剧本里真实存在，不能是凭空的意象
- 不透底：标题不得说破真相、不得点出神话存在的名字、不得预告结局。读到标题的玩家应当只知道故事大概发生在什么地方、围绕什么东西展开
- 忌讳用词：不用"低语/回响/深渊/阴影/凝视/苏醒/沉睡/诅咒/禁忌/邪恶"这类被用滥的氛围词——它们套在任何剧本上都成立，因而不指向任何剧本；也不用"事件/事故/案件/报告/记录/始末"这类把标题写成公文或流水账的词
- 不合格的例子（这三类一律重写）：
  · "布里格斯农场赔了三栏羊"——主谓宾齐全的事件陈述，像一条事故流水记录
  · "深渊的低语"——空洞氛围词堆叠，换哪个剧本都能用
  · "神秘的失踪案"——抽象形容词加公文腔
- 合格的形式（只给结构，不给用词）：〈专名〉+〈具体物件〉、〈专名〉的〈具体物件〉、〈时节〉的〈具体事物〉、单独一个〈器物/典籍/地点专名〉。用本剧本自己的词去填这些结构，不得复用本节任何一行里出现过的词`

const titleAgentSystemPrompt = `<role>COC7模组标题拟定</role>
<task>下面给出一份已经写定的COC7模组的核心信息。为它拟定一个标题。core_truth 只供你理解故事，其中的真相不得写进标题。只输出 {"title": "..."} 这一个JSON对象，不解释、不加书名号、不输出任何其他文字。</task>
<title_rules>
` + scenarioTitleRules + `
</title_rules>
<blacklist_rule>
<scenario_title_blacklist> 是本站近期已有模组的标题。你拟的标题不得与其中任何一个相同，也不得是其中某个的同义换词改写。
</blacklist_rule>`

const (
	titleMinRunes    = 4
	titleMaxRunes    = 12
	maxTitleRetries  = 3
	titlePunctuation = "。，、；！？…,;!?"
)

// titleClicheWords 是判死的滥用氛围词与公文腔用词，与 scenarioTitleRules 的忌讳用词一致。
var titleClicheWords = []string{
	"低语", "回响", "深渊", "阴影", "凝视", "苏醒", "沉睡", "诅咒", "禁忌", "邪恶",
	"事件", "事故", "案件", "报告", "记录", "始末",
}

// validateScenarioTitle 返回标题不合格的原因；合格时返回空串。
// 返回值直接拼进给模型的重写指令，所以写成可执行的整改要求而不是判据代号。
// 入参 title 应当已过 normalizeScenarioTitle。
func validateScenarioTitle(title string, blacklist []string) string {
	if title == "" {
		return "标题不能为空"
	}
	if n := len([]rune(title)); n < titleMinRunes || n > titleMaxRunes {
		return fmt.Sprintf("标题当前 %d 字，要求 %d 到 %d 个汉字", n, titleMinRunes, titleMaxRunes)
	}
	if idx := strings.IndexAny(title, titlePunctuation); idx >= 0 {
		return "标题里不能出现句读标点"
	}
	for _, word := range titleClicheWords {
		if strings.Contains(title, word) {
			return fmt.Sprintf("标题里的「%s」是被用滥的词，换成剧本内真实存在的专名或器物名", word)
		}
	}
	for _, used := range blacklist {
		if normalizeScenarioTitle(used) == title {
			return "标题与本站已有模组重名"
		}
	}
	// NOTE: 软判据。"了"在中文模组名里出现率极低，出现时基本都是句子化标题；
	// "着""过"作普通用字（沿着/过桥）误杀率高，故不查。命中只触发重写，不判死。
	if strings.Contains(title, "了") {
		return "标题写成了一句陈述句（出现了体标记「了」），改写成名词性短语"
	}
	return ""
}

// buildTitlePayload 送审定稿模组的核心信息：足够让模型理解故事围绕什么展开，
// 又不必送整篇故事文档——上下文越小，模型在标题这一件事上的注意力越集中。
func buildTitlePayload(draft *ScenarioDraft) map[string]any {
	sceneNames := make([]string, 0, len(draft.Content.Scenes))
	for _, s := range draft.Content.Scenes {
		sceneNames = append(sceneNames, s.Name)
	}
	npcNames := make([]string, 0, len(draft.Content.NPCs))
	for _, n := range draft.Content.NPCs {
		npcNames = append(npcNames, n.Name)
	}
	endingNames := make([]string, 0, len(draft.Content.Endings))
	for _, e := range draft.Content.Endings {
		endingNames = append(endingNames, e.Name)
	}
	coreTruth := ""
	if ka := draft.Content.KeeperAppendix; ka != nil {
		coreTruth = ka.CoreTruth
	}
	return map[string]any{
		"current_title": draft.Name,
		"setting":       draft.Content.Setting,
		"invest_focus":  draft.Content.InvestFocus,
		"mythos_anchor": draft.Content.MythosAnchor,
		"scenes":        sceneNames,
		"npcs":          npcNames,
		"endings":       endingNames,
		"core_truth":    coreTruth,
	}
}

// runTitleAgent 在隔离上下文里为定稿草稿拟定标题。
// 不合格的候选会带着具体原因发回重写，最多 maxTitleRetries 轮；轮次用尽后返回最后一个
// 非空候选而不是报错——标题不合格是质量问题，不是致命错误。
// 只有在没有可用 provider 或一个非空候选都拿不到时才返回 error，此时调用方保留编译标题。
func runTitleAgent(ctx context.Context, room *scripterRoom, draft *ScenarioDraft) (string, error) {
	if draft == nil {
		return "", fmt.Errorf("title agent: draft is nil")
	}
	provider := room.architect
	if provider.provider == nil {
		provider = room.compiler
	}
	if provider.provider == nil {
		return "", fmt.Errorf("title agent: no LLM provider available")
	}
	sessionID := scripterSessionID(ctx, room)

	payloadJSON, err := json.Marshal(buildTitlePayload(draft))
	if err != nil {
		return "", fmt.Errorf("title agent: marshal payload failed: %w", err)
	}

	// 隔离上下文：全新消息链，不与主流水线共享历史。
	msgs := []llm.ChatMessage{
		{Role: "system", Content: provider.systemPrompt(titleAgentSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf(`<module_summary>%s</module_summary>
<scenario_title_blacklist>
%s
</scenario_title_blacklist>
请为以上模组拟定标题，只输出 {"title": "..."}。`, string(payloadJSON), formatScenarioTitleBlacklist(room.titleSamples))},
	}

	// NOTE: cacheKey 追加 :title 后缀，与 architect 主链（故事阶段）的 prompt cache 隔离——
	// 两者 system prompt 完全不同，共用一个 key 会互相污染缓存前缀。
	cacheKey := provider.cacheKey(sessionID) + ":title"
	lastCandidate := ""

	for attempt := 1; attempt <= maxTitleRetries; attempt++ {
		stage := fmt.Sprintf("title_attempt_%d", attempt)
		logStagePrompt(stage, sessionID, msgs)
		resp, err := provider.provider.JsonChat(ctx, cacheKey, msgs)
		if err != nil {
			log.Printf("[scripter:title] session=%s attempt=%d chat failed: %v", sessionID, attempt, err)
			break
		}
		recordScripterLLMExchange(ctx, nil, stage, msgs, resp)

		var parsed struct {
			Title string `json:"title"`
		}
		reason := ""
		candidate := ""
		if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
			reason = `输出必须是 {"title": "..."} 格式的JSON，不要附带任何其他文字`
		} else {
			candidate = normalizeScenarioTitle(parsed.Title)
			reason = validateScenarioTitle(candidate, room.titleSamples)
		}
		if candidate != "" {
			lastCandidate = candidate
		}
		if reason == "" {
			log.Printf("[scripter:title] session=%s attempt=%d accepted title=%q", sessionID, attempt, candidate)
			return candidate, nil
		}

		log.Printf("[scripter:title] session=%s attempt=%d rejected title=%q reason=%s", sessionID, attempt, candidate, reason)
		msgs = append(msgs,
			llm.ChatMessage{Role: "assistant", Content: resp},
			llm.ChatMessage{Role: "user", Content: fmt.Sprintf(`SYSTEM REJECT: 上一个标题不合格——%s。请重新给出一个标题，只输出 {"title": "..."}。`, reason)},
		)
	}

	if lastCandidate == "" {
		return "", fmt.Errorf("title agent: 连续%d次未取得可用标题", maxTitleRetries)
	}
	log.Printf("[scripter:title] session=%s 重试用尽，采用最后一个候选 title=%q", sessionID, lastCandidate)
	return lastCandidate, nil
}
