// NOTE: 编译阶段（Compiler）：把 Story Architect 或管理员上传的故事全文忠实转成结构化 ScenarioDraft。
// compiler 在 ReAct 工具循环里按字段调用工具修改内存草稿（见 scripter_compile_draft.go），
// 最后用 submit_draft 过提交关卡。逻辑审查发现的问题追加到同一条对话里，由同一个 compiler 用同一套工具局部修改。
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

const (
	// NOTE: 轮数上限没有数据依据：一轮可并行调用多个编辑工具，30 轮足够写完一份模组，15 轮足够按问题清单局部修改。
	compileMaxRounds    = 30
	compileFixMaxRounds = 15
	// NOTE: 提交被拒的次数上限；超过后带着剩余问题放行，由后续的归一化兜底，避免个别无法消除的问题让整次编译白费。
	compileMaxRejects = 3
)

// compilerSystemPrompt 定义编译器角色：只做故事文档到结构化数据的忠实转换，不创作新事实。
func compilerSystemPrompt() string {
	return `<role>COC7剧本编译器</role>
<task>
你不是剧本创作者，而是格式编译器。<story_document> 是一份已经写定的完整COC7模组成稿，其中的真相、地点、人物、线索、时间线与结局均已确定。你的唯一任务是把这些既定事实忠实地转换成结构化数据，逐块写入草稿，严禁改写、新增、删减或重新设计任何情节、人物、线索、结局或神话锚点。

【关于故事文档的形式】
成稿是按职业出版模组的写法组织的，不是按结构化字段分类整理的：它通常沿调查员实际会走的地理或时间顺序排篇，使用章节、地点或事件标题；人物写在他所在的那一段里，线索写在能拿到它的那一处，检定和后果写在可执行的地点或遭遇段落里。时间线、装备、遭遇流程以及角色和怪物数据可以采用短列表、表格或数据块。文中可能有"守密人信息""可选方案"等明确面向KP的段落，也可能把提示就地插入。不要因为文本包含标题、编号、列表、规则数值或数据表，就把它误判成设计文档；真正需要排除的是字段式标签、内部设计术语和无法运行的空泛描述。

所以你要做的是通读全文、理解脉络之后做语义识别，而不是只按小节标题或字段标签做匹配：自己判断哪一段描写的是一处调查员会去的地方、哪几句介绍的是一个人、哪一句给出的是一条可获得的信息、哪一段是一种收场。同一处地点的信息可能散落在几个段落里（例如某个人物的秘密在别处才被揭穿），要合并；一个小标题下也可能同时包含地点描写、人物介绍、多条线索和守密人提示，要拆开。绝不能因为文中使用出版模组惯用的章节与数据排版，就把某个字段留空。

允许的合理补全仅限于：故事文档未写明具体数值时（如NPC属性），按COC7规则书惯例给出合理数值；故事文档地点/NPC无英文标识时，为scene.id生成snake_case标识符。这类补全不得引入新的事实或改变已有事实。

【字段映射规则】括号里是写入该字段用的工具
- name（set_overview）：照抄故事文档的标题；文档未给出明确标题时，取文档中出现的一个地名或器物名填入，不要写成一句话（后续有专门环节负责定稿标题）
- tags（set_overview）：2-3个逗号分隔标签，指向本剧本独有的核心叙事装置或桥段，不用抽象风格词；须避开<recent_scenario_tags_blacklist>
- setting（set_overview）：取自故事文档的表层情境原文或忠实改写，必须保留其中嵌入的具体年月日（如"1923年10月15日"）；该字段同时作为模组对外展示的简介
- intro（set_overview）：以完整的叙事散文呈现调查员到场的具体情境——取自故事文档的调查员到场情境与基本理由，不能只交代抽象事实、要让人读出画面，让这段文字读起来像小说开篇，而不是照抄原句压缩出的说明性短句。写作顺序须先交代背景与任务、再展开画面：开篇先用自然的叙事句子交代调查员此刻所在的具体场所（精确到建筑/门牌/房间，不能只写城市）、具体年月日+时刻（年月日须与setting一致，时刻须与game_start_time里的时刻一致）、以及调查员被委托或卷入要达成的那件事（只写要达成的事本身，不写达成它的手段与步骤，不写"先去问谁""应该搜查哪里""建议从X入手"），随后再在不新增事实的前提下扩写出至少两处能让读者看见、听见、感受到的具体场景细节（环境陈设、光线声响、旁人动作、只言片语的对话等，不能只写"一切安静而寻常"这类空泛概括）。以上四项信息须自然融入句子，不得另起一行、不得使用【】或"地点：/时间：/目标："这类状态栏式标签罗列；场景细节允许在故事文档基础上做感官化的扩写呈现，其余三项须取自故事文档已写明的内容、不得新增事实；全部不得剧透真相或渲染恐怖氛围；除此之外仍不列出、不推荐、不暗示任何具体行动或下一步
- tone_tags（set_overview）：用户消息里有<fixed_tone_tags>时，系统已预填，不要改动；没有时，用2-4个短语概括故事文档已确立的文风与氛围，忠实提炼
- invest_focus（set_overview）：通读全文后，用一个简短短语概括故事文档已确立的调查入口（调查员最初从哪一类异常介入），忠实提炼，不得自创文档中不存在的入口
- mythos_anchor：系统已按<mythos_anchor>预置，没有工具可以改它，不需要也不要填写
- scenes（upsert_scenes）：通读全文，识别出所有调查员会实际走到的地方（无论它是否有独立小标题、无论按什么顺序出现），每处编译为一个scene；scene.id为snake_case英文标识；description要把该地点在文中散落的全部信息合并写全——进门能直接看见和感知到什么、深入查探能发现什么以及需要什么检定、检定成功与失败分别造成什么后果、这里可能发生什么危险、从这儿能通往哪些地方；出版稿中写明的规则数值和遭遇步骤要保留在description，不要只留下氛围概括；triggers默认["available_from_start"]，仅当文中叙述明确表示需要先获得某个发现或完成某个行动才会来到此处时，才使用条件触发
- npcs（upsert_npcs）：通读全文，识别出所有有名有姓、调查员可能接触到的人物（他们通常介绍在其所在地点的段落里，而不是集中成名单），每位编译为一个npc；description要把该人物在文中散落的信息合并写全——他的身份与营生、他想要什么、他正在做什么、他瞒着或不愿说的事、他的标志性小细节、他与其他人的关系；文中若明确写出他隐瞒或保留了某些信息，description须把这一点写明；stats按COC7规则书惯例给出合理属性值（含SAN、HP、MP），文中已给出数值表时直接采用；attitude取自文中写明或可直接判断的初始态度；skills按该人物的职业/角色身份给出3-6项最相关的技能及数值（COC7标准范围，普通人类技能值通常15-75）；spells仅文中明确写明会施法的人物才填写，普通人类不填
- clues（upsert_clues）：通读全文，识别出所有调查员能亲自获得的具体发现——一份文件、一句证词、一处痕迹、一个检定结果、一件物品——每条编译为一条线索。这些发现在文中通常写在地点、人物或守密人说明的叙事段落里，没有单独的清单，你需要自己把它们摘出来。key是你自起的短标识，只用于后续覆盖或删除，不会进入成品。出版稿中明确写出的技能、难度、伤害、理智损失或其他规则数值应忠实保留在对应字段；nature由你根据文意判定："真实"指调查员可直接查证、指向主线的观察；"隐藏"指揭示神话存在本身是什么的那类发现；"误导"指本身真实、却被文中某人给出了看似合理却错误的解释、从而会把调查引向无关结论的那类发现。source填该发现来自哪个地点、哪个人或哪件物品；skill_check/on_success/on_failure按文中写明的检定名、难度与成败后果对应填写，文中未写明时可留空
- mythos_core（set_keeper_notes）：神话本质的核心揭示，即这个故事里神话真相本身是什么，与mythos_anchor强绑定。它是KP独有的材料，永远不会通过线索暴露给玩家，所以只写在这里，不得再把同样的揭示写成一条线索
- endings（upsert_endings）：通读全文，识别出所有互不相同的收场（通常集中在结尾附近，但也可能散见于"如果调查员没有……"这类叙述中），每种编译为一个ending；文中已给出收场名称的直接采用，未命名的按其内容取一个名字；trigger改写为"如果[条件]，则[处境变化]"的条件句结构；san_reward由你依据文中描述的理智冲击轻重程度对照COC7规则书惯例换算出一个具体骰值，出版稿已明确写出的SAN/伤害数值优先采用；is_failure标记灾难/失败向的收场；每一种独立的收场都要对应一个ending，不得合并或省略
- game_start_time（set_overview）：从故事文档嵌入的具体年月日和时刻写成 RFC 3339 本地时间 YYYY-MM-DDTHH:MM，不带时区；年月日须与setting一致；文档未写明具体时刻时取08:00
- map_description（set_overview）：根据故事文档的地点关系概括为文字地图，体现可回访、可交叉验证的调查网络
- playthrough_outline（set_overview）：由你依据全文脉络归纳出一份逐场景的游玩流程大纲，供KP按此把握主线。故事文档不会有独立的"游玩大纲"小节——路线、解锁条件与分支是写在正文叙述里的（"一个更简单前往矿井的方法是开卡车沿路上去""如果调查员先去了X，则……"），你需要把它们抽出来串成流程：开场调查员如何进入第一个场景；此后每个场景的进入条件（默认开局即可进入，或需先获得什么发现、完成什么行动）、在此能接触到的人物、能获得的发现、以及从这里通向哪些场景（有分支写清"若…则…"）；最后是哪些条件分别导向哪种收场。大纲中出现的场景、人物、发现与收场必须全部来自已写入草稿的内容，不得引入文档中不存在的东西，所以应当在场景、人物、线索和结局写完之后再写它
- timeline（set_timeline）：通读全文，识别出所有已发生事件与当天推进的时间节点（它们可能集中在一段时间线里，也可能散见于叙述中），逐条提取为{time,event,phase:past|current}；event须写成中性事实记录句（谁在何时做了什么、什么状态发生了变化）；若故事文档以对话、引语或文学化叙事呈现该时间节点，只转述句式、不引用人物原话，事实内容本身不得改变；文档确实没有可提取的时间节点则不要调用
- keeper_appendix（set_keeper_notes）：core_truth必填，复述故事文档的核心真相与mythos_anchor对故事的必要性（即为何不可替换）；antagonist_dossier在故事文档写明了施动者时必填——邪教组织的名称伪装/教义/仪式/结构/招募控制/经济据点/历史渊源，或个人施法者的身份掩护/接触契机/法术能力/终极目的，或神话生物的来历/栖身范围/可观察影响/行为驱动，须完整保留，不得压缩为一句话；文档确无独立施动者可写时不填。其余6个运营建议子字段（difficulty_down/difficulty_up/solo_advice/group_advice/horror_tips/theme_guidance）通读全文识别难度调节、单双人团建议或恐怖呈现提示（可能是集中一段，也可能是散在各处对守密人说的话）归拢填入；给守密人的建议在成稿中常以"守密人应当……"这类口语化提示直接插在对应段落里，需要你把它们归拢到对应子字段；文档没有对应内容的子字段不填
- mechanics（upsert_mechanics）：通读全文，识别出是否描述了可量化追踪的机制（如计数器、行动时钟）（可能是集中一段，也可能是散在各处），提取为{name,type:counter|clock|tracker,description,stages:[{label,effect,trigger}]}；这些机制仅供KP参考，不做自动结算；文档未设计此类机制则不要调用
- reward_concept（set_overview）：本篇的通关奖励概念，由你设计，不是从原文摘抄。故事文档通常不会写"奖励"二字，但它的世界里必然存在与真相相关的实物。通读全文后，从文中已确立的施动者、地点、人物与神话锚点里指认（或据此合成）一件调查员在非失败结局后能带走的实体载体——邪教用于仪式的器物、施法者留下的笔记与抄本、死者遗物中那册来路不明的书、神话生物栖身处的遗存等——写成一句话叙事概念，说清它是什么、原本属于谁或存放在哪、与核心真相或mythos_anchor是什么关系。硬性边界：只能建立在文档已有的人、地、事、物之上，不得为它新增人物、新增地点、改动结局条件或推翻文中已写明的事实（文中已明写被彻底销毁的物件不得用作奖励）；必须是可携带的实体（典籍/手稿/器物），不得写成"知识""顿悟""名望""某人的信任"这类交不到调查员手上的抽象收获；不写具体规则数值、SAN代价与技能加值（那由后续的奖励设计专家依规则书裁定）；若文档本身已明确写出通关奖励，则原样提炼、不另行设计。本字段必须给出，不得留空

【硬性约束】
- 不得编造、合并或删除故事文档中不存在的人名、地名、事件、线索或结局
- 不得改变故事文档已确定的真相、神话锚点、误导线索的设计意图或结局条件
- mythos_core 与 nature="隐藏"的线索中出现的法术名/物品名/怪物名/材质名必须与<mythos_anchor>及故事文档一致，不得新造规则书中不存在的元素
- setting/intro必须保持故事文档表层情境的冷开场语气：中性日常，不剧透真相、不渲染恐怖、不出现惊悚诡异词汇
- setting优先照抄故事文档表层情境的原句；确需压缩时只做删句，不改写句式、不把多句合并成一个塞满信息的长句
- intro须先自然写出地点/时间/目标三项背景信息，再在不新增事实的前提下把故事文档的表层情境改写、扩写成带具体场景细节的叙事段落（至少两处可视/可听/可感的细节，不能只写"一切如常"这类空泛概括）；地点/时间/目标/场景细节四项信息必须自然写进叙事句子里，禁止另起一行、禁止使用【】或"地点：/时间：/目标："这类状态栏式标签
- nature="真实"的线索至少2条且互相独立可组合；不得只编译出单一线索链
- 至少一位NPC的description须写明"秘密"或"保留"信息
</task>
<tools>
- set_overview / set_keeper_notes：只覆盖传入的字段，未传的保持不变
- upsert_scenes / upsert_npcs / upsert_clues / upsert_endings / upsert_mechanics：按键写入，已有的键整条覆盖，新键追加；一次可带多条
- set_timeline：整体替换时间线
- remove_entries：按键删除列表条目
- view_draft：查看草稿的填写情况和此刻提交会遇到的问题；带section可读回该部分全文
- ask_lawyer：核对规则书里的怪物属性、法术效果、技能检定；故事文档已写明的内容不要重复核验
- submit_draft：提交草稿，必须单独一轮调用
</tools>
<workflow>
1. 先通读<story_document>全文，不要急着写。
2. 用编辑工具逐块写入。一次调用可以带多个条目，一轮可以同时调用多个编辑工具，尽量减少轮数；写入工具只回简短回执，不回显内容。
3. 写完后调用view_draft，对照问题清单补漏。
4. 单独一轮调用submit_draft。未通过时回执会列出问题清单：只改问题涉及的内容，不要重写已经正确的部分，改完再提交。
</workflow>`
}

// compileSession 持有一次编译的内存草稿和与 compiler 的对话。编译和逻辑修复共用同一条对话，
// 这样修复时 compiler 仍然看得到故事全文和自己先前的写入。
type compileSession struct {
	room    *scripterRoom
	handle  agentHandle
	conv    *scripterConversation
	draft   *compileDraft
	rejects int
}

// startCompile 建立对话并跑到提交被接受。compiler 未配置或被禁用时回落到 architect。
func startCompile(ctx context.Context, room *scripterRoom, story StoryOutput, constraints ScripterConstraints) (*compileSession, error) {
	handle := room.compiler
	if handle.provider == nil {
		handle = room.architect
	}
	if handle.provider == nil {
		return nil, fmt.Errorf("compiler/architect provider unavailable")
	}
	s := &compileSession{
		room:   room,
		handle: handle,
		draft:  newCompileDraft(story, constraints),
	}
	s.conv = newScripterConversation(
		llm.ChatMessage{Role: "system", Content: handle.systemPrompt(compilerSystemPrompt())},
		llm.ChatMessage{Role: "user", Content: compileUserMessage(story, s.draft, room.tagsBlacklist)},
	)
	if err := s.run(ctx, "compile", compileMaxRounds); err != nil {
		return nil, err
	}
	d := s.draft.draft
	alog.Debug("scripter compile done", "session", scripterSessionID(ctx, room), "name", d.Name, "scenes", len(d.Content.Scenes), "npcs", len(d.Content.NPCs), "clues", len(d.Content.Clues), "rejects", s.rejects)
	logScripterArtifact("Compiled ScenarioDraft", scripterSessionID(ctx, room), d)
	return s, nil
}

func compileUserMessage(story StoryOutput, d *compileDraft, tagsBlacklist []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<story_document>%s</story_document>\n<mythos_anchor>%s</mythos_anchor>\n", story.Document, story.MythosAnchor)
	if len(d.fixedToneTags) > 0 {
		fmt.Fprintf(&sb, "<fixed_tone_tags>%s</fixed_tone_tags>\n", strings.Join(d.fixedToneTags, ", "))
	}
	fmt.Fprintf(&sb, "<recent_scenario_tags_blacklist>\n%s\n</recent_scenario_tags_blacklist>\n", formatScenarioTagsBlacklist(tagsBlacklist))
	sb.WriteString("请通读以上故事文档，用工具把它逐块编译进草稿，最后单独一轮调用 submit_draft 提交；tags 须避开 recent_scenario_tags_blacklist 中的标签。")
	return sb.String()
}

// fix 把逻辑审查发现的问题追加到同一条对话，让 compiler 用同一套工具改草稿并重新提交。
// 失败时草稿回到调用前的状态。
func (s *compileSession) fix(ctx context.Context, issues []string) error {
	snapshot := s.draft.clone()
	s.rejects = 0
	var sb strings.Builder
	sb.WriteString("<must_fix>\n")
	for i, issue := range issues {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, issue)
	}
	sb.WriteString("</must_fix>\n逻辑审查以故事文档为唯一真相源，发现了以上问题。用编辑工具逐条修复：只改问题涉及的内容，已经正确的部分保持不动，不得新增故事文档里没有的事实；规则书事实不确定时先 ask_lawyer。改完后调用 view_draft 自查，再单独一轮调用 submit_draft 提交。")
	s.conv.append(llm.ChatMessage{Role: "user", Content: sb.String()})
	if err := s.run(ctx, "compile_fix", compileFixMaxRounds); err != nil {
		s.draft = snapshot
		return err
	}
	return nil
}

// result 返回草稿副本和 reward_concept；调用方对副本做的改动不会回到会话里。
func (s *compileSession) result() (ScenarioDraft, string) {
	c := s.draft.clone()
	return c.draft, strings.TrimSpace(c.rewardConcept)
}

func (s *compileSession) run(ctx context.Context, stage string, maxRounds int) error {
	_, err := runToolLoop(ctx, toolLoopOptions{
		room:      s.room,
		handle:    s.handle,
		stage:     stage,
		tools:     compileDraftTools(),
		maxRounds: maxRounds,
		dispatch:  s.dispatch,
		conv:      s.conv,
	})
	return err
}

func (s *compileSession) dispatch(ctx context.Context, call llm.ToolCall) toolOutcome {
	if call.Name == toolNameSubmitDraft {
		return s.submit(ctx)
	}
	return s.draft.dispatch(ctx, s.room, call)
}

// submit 是提交关卡：问题清单为空即通过；否则最多拒绝 compileMaxRejects 次，之后带着剩余问题放行。
func (s *compileSession) submit(ctx context.Context) toolOutcome {
	issues := s.draft.gateIssues(s.room.tagsBlacklist)
	if len(issues) == 0 {
		return toolOutcome{result: "草稿已通过校验并提交。", done: true}
	}
	sessionID := scripterSessionID(ctx, s.room)
	if s.rejects >= compileMaxRejects {
		alog.Warn("scripter compile submitted with remaining issues", "session", sessionID, "rejects", s.rejects, "issues", issues)
		s.room.emitProgress("compile", "start", fmt.Sprintf("提交校验仍有 %d 个问题，已达重试上限，按当前草稿继续", len(issues)))
		return toolOutcome{result: fmt.Sprintf("已达提交重试上限，带着剩余 %d 个问题放行；后续环节会兜底。", len(issues)), done: true}
	}
	s.rejects++
	alog.Warn("scripter compile submit rejected", "session", sessionID, "reject", s.rejects, "issues", issues)
	s.room.emitProgress("compile", "start", fmt.Sprintf("提交校验未通过（第 %d 次）：还有 %d 个问题，继续修改", s.rejects, len(issues)))
	var sb strings.Builder
	fmt.Fprintf(&sb, "SYSTEM REJECT: 草稿未通过提交校验，共 %d 个问题（第 %d/%d 次拒绝）。用编辑工具只改问题涉及的内容，不要重写已经正确的部分，改完再单独一轮调用 submit_draft：\n", len(issues), s.rejects, compileMaxRejects)
	for i, issue := range issues {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, issue)
	}
	return toolOutcome{reject: strings.TrimRight(sb.String(), "\n")}
}
