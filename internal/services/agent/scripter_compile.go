// scripter_compile.go — Compiler stage: takes the story architect's free-text
// StoryOutput (scripter_story.go) and compiles it into a structured
// ScenarioDraft (OneshotResult shape), without inventing new facts. Runs as a
// single LLM call (no tool loop); structural/logic issues found afterwards
// are still patched by the existing repairOneshotDraft loop.
package agent

import (
	"context"
	"fmt"
	"log"

	"github.com/llmcoc/server/internal/services/llm"
)

// compilerSystemPrompt 定义编译器角色：只做故事文档到结构化JSON的忠实转换，不创作新事实。
func compilerSystemPrompt() string {
	return `<role>COC7剧本编译器</role>
<task>
你不是剧本创作者，而是格式编译器。<story_document>是已经写定的完整COC7剧本故事文档，其中的真相、地点、NPC、线索、时间线与结局均已确定。你的唯一任务是把这些既定事实忠实地转换成结构化JSON，严禁改写、新增、删减或重新设计任何情节、人物、线索、结局或神话锚点。

允许的合理补全仅限于：故事文档未写明具体数值时（如NPC属性），按COC7规则书惯例给出合理数值；故事文档地点/NPC无英文标识时，为scene.id生成snake_case标识符。这类补全不得引入新的事实或改变已有事实。

【字段映射规则】
- name：取故事文档标题；若文档未给出明确标题，从文档内具体名词（地名/物件/日期/一句当地话）提炼一个像人类作者起的标题，不用"低语/回响/深渊/阴影/凝视/苏醒/沉睡/诅咒"等滥用词
- description：剧本简介，取自或忠实改写故事文档的表层情境段落；中性、不剧透，读者看不出剧情走向
- content.setting：取自故事文档的表层情境原文或忠实改写，必须保留其中嵌入的具体年月日（如"1923年10月15日"）
- content.intro：取自故事文档中调查员到场情境与基本理由；不列出、不推荐、不暗示任何具体行动或下一步
- content.horror_mode / content.invest_focus / content.tone_tags：必须逐字等于<diversity_constraints>中的对应值，不得自行替换
- content.mythos_anchor：必须逐字等于<mythos_anchor>输入
- content.scenes：从故事文档的地点部分逐个提取；每个scene.id为snake_case英文标识；description须完整保留可见信息/可发现信息/杠杆/风险/出口/感官细节；triggers默认["available_from_start"]，仅当故事文档明确写出解锁条件时才使用条件触发
- content.npcs：从故事文档的NPC部分逐个提取；description须完整保留公开身份/议程/秘密/标志性细节/关系网；stats按COC7规则书惯例给出合理属性值（含SAN、HP、MP）；attitude取自故事文档写明的初始态度
- content.clues：从故事文档的线索部分逐条提取为结构化对象{summary,source,skill_check,on_success,on_failure,nature}；nature必须是"真实"/"隐藏"/"误导"之一；summary保留来源事实/支持命题等关键信息；skill_check/on_success/on_failure按故事文档写明的检定与推进逐条对应，文档未写明时可留空；至少一条nature="隐藏"的线索须包含"神话本质"字样并与mythos_anchor强绑定
- content.endings：从故事文档的结局部分逐个提取为{name,trigger,description,san_reward,is_failure}；trigger保持"如果[条件]，则[处境变化]"的条件句结构；san_reward取自文档写明的SAN恢复/损失（如"恢复1d6"），文档未写明时按结局性质给出合理数值；is_failure标记灾难/失败向结局；故事文档中每一个独立结局都要对应一个ending，不得合并或省略
- content.system_prompt：包含KP独有的内部真相（复述故事文档的核心真相与mythos_anchor对故事的必要性，即为何不可替换）以及时间推进/信息分层/不主动引导三项KP协议
- content.game_start_slot：从故事文档嵌入的具体时刻推算（0-47，每槽30分钟）；文档未写明具体时刻时取16
- content.map_description：根据故事文档的地点关系概括为文字地图，体现可回访、可交叉验证的调查网络
- content.handouts：若故事文档包含可直接朗读给玩家的手卡/信件/剪报等原文材料，逐条提取为{title,content,timing}；文档未提供此类材料则content.handouts留空数组，不得虚构
- content.timeline：若故事文档写明了过去线痕迹或当天推进的时间节点，逐条提取为{time,event,phase:past|current}；文档未写明明确时间线则留空数组
- content.keeper_appendix：若故事文档包含难度调节、单双人团建议或恐怖呈现提示，提取为{difficulty_down,difficulty_up,solo_advice,group_advice,horror_tips,theme_guidance}；文档未提供则整体省略（null）
- content.entry_identities：若故事文档为不同职业调查员写明了差异化的入场方式，逐条提取为{profession,init_resource,init_limit,recommend_clues}；文档未区分职业入场则留空数组
- content.mechanics：若故事文档描述了可量化追踪的机制（如计数器、行动时钟），提取为{name,type:counter|clock|tracker,description,stages:[{label,effect,trigger}]}；这些机制仅供KP参考，不做自动结算；文档未设计此类机制则留空数组
- reward_concept：逐字取自<reward_concept>输入，不改写、不留空（输入为空则留空字符串）

【硬性约束】
- 不得编造、合并或删除故事文档中不存在的人名、地名、事件、线索或结局
- 不得改变故事文档已确定的真相、神话锚点、误导线索的四要素设计或结局条件
- [隐藏]神话本质说明中出现的法术名/物品名/怪物名/材质名必须与<mythos_anchor>及故事文档一致，不得新造规则书中不存在的元素
- description/content.setting/content.intro必须保持故事文档表层情境的冷开场语气：中性日常，不剧透真相、不渲染恐怖、不出现惊悚诡异词汇
- content.clues中nature="真实"的线索至少2条且互相独立可组合；不得只编译出单一线索链
- 至少一位NPC的description须写明"秘密"或"保留"信息
</task>
<response_format>json_object</response_format>
<output>只输出一个合法JSON对象，字段结构严格匹配<schema_example>；不要Markdown、标题、解释或代码围栏。</output>`
}

// compileStoryToModule 把故事 architect 的自由文本 StoryOutput 编译为结构化 ScenarioDraft。
// compiler 未配置时 fallback 到 architect provider；编译只做一次LLM调用（非工具循环），
// 解析失败会经由 chatAndParseJSON 复用既有的 JSON 修复链。
func compileStoryToModule(ctx context.Context, room *scripterRoom, story StoryOutput, constraints ScripterConstraints) (ScenarioDraft, error) {
	compiler := room.compiler
	if compiler.provider == nil {
		compiler = room.architect
	}
	if compiler.provider == nil {
		return ScenarioDraft{}, fmt.Errorf("compiler/architect provider unavailable")
	}
	sessionID := scripterSessionID(ctx, room)

	userMsg := fmt.Sprintf(
		`<story_document>%s</story_document>
<mythos_anchor>%s</mythos_anchor>
<reward_concept>%s</reward_concept>
%s
%s
<recent_scenario_tags_blacklist>
%s
</recent_scenario_tags_blacklist>
<schema_example>%s</schema_example>
请将以上故事文档编译为结构化剧本JSON，严格遵循schema_example的字段结构；tags须避开recent_scenario_tags_blacklist中的标签。`,
		story.Document, story.MythosAnchor, story.RewardConcept,
		diversityConstraintsBlock(constraints),
		proseVoiceBlock(constraints),
		formatScenarioTagsBlacklist(room.tagsBlacklist),
		oneshotExample,
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: compiler.systemPrompt(compilerSystemPrompt())},
		{Role: "user", Content: userMsg},
	}
	logStagePrompt("compile", sessionID, msgs)

	var result OneshotResult
	if err := chatAndParseJSON(ctx, compiler, msgs, &result, oneshotExample, "compile"); err != nil {
		return ScenarioDraft{}, fmt.Errorf("compile failed: %w", err)
	}

	draft := result.toScenarioDraft()
	// NOTE: mythos_anchor 已由 story 阶段 translate_anchor 确认，编译阶段强制覆盖，防止LLM篡改。
	draft.Content.MythosAnchor = story.MythosAnchor
	log.Printf("[scripter:compile] session=%s done name=%q scenes=%d npcs=%d clues=%d",
		sessionID, draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	logScripterArtifact("Compiled ScenarioDraft", sessionID, draft)
	return draft, nil
}
