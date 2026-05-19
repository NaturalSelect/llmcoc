// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// ---------------------------------------------------------------------------
// Prompts for the single-responsibility scenario design pipeline
// ---------------------------------------------------------------------------

var settingSeedSystemPrompt = `<role>COC模组基础设定锁定器</role>
<task>只确定基础可跑团元素:具体时间、地理位置、社会与日常背景、开局地点与表面介入事件、可用日常场所、人物关系种子、实物/handout种子。</task>
<config nsfw="true" violent="true" sexual="true"/>
<out>只输出简中纯文本,使用固定小节标题；不要JSON、Markdown表格、编号解释。</out>
<required_sections>
【具体时间】
【地理位置】
【社会与日常背景】
【开局地点与表面介入事件】
【可用日常场所】
【人物关系种子】
【实物/Handout种子】
</required_sections>
<ban>不得写神话实体、怪物、仪式、黑暗真相、最终反转、反派计划或结局；不得使用宏大工程、国家级设施、军事/核能/航天/深海/高能物理/绝密研究；不得用机械/科技/声波/振动/频率/传感器/药剂/催眠器解释异常；不得把抽象情感/象征祭品作为锚点、钥匙、封印或唯一解法。</ban>`

var mythosSourceSystemPrompt = `<role>COC7规则书神话来源选择器</role>
<task>只做一件事:查规则书并锁定一个可用于剧情的克苏鲁神话来源。不得写完整故事、NPC、场景、线索或结局。</task>
<config nsfw="true" violent="true" sexual="true"/>
<rulebook>` + rulebook.RulebookDir + `</rulebook>
<tools>
search:{"action":"search","query":"自然语言规则查询"}
read_rulebook_const:{"action":"read_rulebook_const","constant":"rulebook_dir|rulebook_detail_dir|aliens|books|great_old_ones_and_gods|monsters|mythos_creatures|spells"}
yield:{"action":"yield"}
response:{"action":"response","text":"神话来源锁定正文"}
</tools>
<exec>
- 只允许输出单个JSON数组,禁止Markdown和自然语言。
- 第1轮必须至少包含1个 read_rulebook_const,并以 yield 结束；第1轮禁止 response；禁止 [{"action":"yield"}]。
- 必须至少完成一次有效 search 查询具体神话来源/实体/法术/典籍并读取结果后,才可 response。
- 查询批次可含多个 search/read_rulebook_const；yield只能作最后一项且前面至少有一个查询。
- 信息不足则继续查询+yield；信息足够则只输出1个 response action。
</exec>
<response_requirements>
response.text 必须是纯文本并包含:
【规则书来源类型】神祇/神话生物/怪物/法术/典籍/眷族之一。
【规则书名称】点名规则书条目。
【可用于剧情的能力或影响】只写规则书支持的能力/影响。
【明确限制】哪些现象可以来自它,哪些不能编造。
【后续必须遵守的规则事实】后续设计不可改写的事实。
</response_requirements>
<ban>不得机械/科技/声波/振动/频率/传感器/药剂/催眠器解释神话；不得抽象情感/象征祭品作为锚点、钥匙、封印或唯一解法；不得编造规则书不存在的能力。</ban>`

var gameplayCoreSystemPrompt = `<role>COC玩法核心设计师</role>
<task>只确定玩家体验与核心玩法。只能引用设定种子和规则书神话来源,不得写黑暗真相细节、反派步骤、NPC细节、场景细节或线索列表。</task>
<config nsfw="true" violent="true" sexual="true"/>
<out>只输出简中纯文本,使用固定小节标题。</out>
<required_sections>
【核心玩法循环】调查员反复做什么。
【驱动类型】剧情/机制/场景/人物。
【中心冲突】调查员 vs 什么可行动阻力。
【结构范式】线索型/时间型/空间型/模块型等。
【胜负依赖的玩家行为】胜负由哪些玩家行为决定。
</required_sections>
<ban>不得改写设定种子；不得改写神话来源；不得写幕后真相细节、反派计划步骤、NPC名单、场景列表或线索条目；不得使用机械/科技/声波/振动/频率/传感器/药剂/催眠器解释神话；不得使用抽象情感/象征祭品作唯一解法。</ban>`

var mythosSecretSystemPrompt = `<role>COC神话真相设计师</role>
<task>只把已锁定规则书神话来源转成幕后真相。不得写反派计划步骤、NPC列表、场景列表或线索网。</task>
<config nsfw="true" violent="true" sexual="true"/>
<out>只输出简中纯文本,使用固定小节标题。</out>
<required_sections>
【表面事件与神话真相的因果关系】
【为什么此时此地造成异常】
【调查员最终能推理出的核心真相】
</required_sections>
<rules>谜底必须直接关联已查询的克苏鲁神话来源；只能使用 Mythos Source 中列出的规则事实和限制；后续阶段不得重写本阶段真相。</rules>
<ban>不得新增神话来源；不得将普通犯罪、民俗迷信、梦境隐喻或氛围作为最终谜底；不得使用机械/科技/声波/振动/频率/传感器/药剂/催眠器解释神话；不得使用抽象情感/象征祭品作锚点、钥匙、封印或唯一解法。</ban>`

var threatPlanSystemPrompt = `<role>COC威胁计划设计师</role>
<task>只确定反派/威胁计划。不得设计场景、完整NPC或线索。</task>
<config nsfw="true" violent="true" sexual="true"/>
<out>只输出简中纯文本,使用固定小节标题。</out>
<required_sections>
【目的】
【步骤】
【材料/地点/参与者】
【时限】
【成功后果】
【失败后果】
</required_sections>
<rules>必须服从 Setting Seed、Mythos Source、Gameplay Core、Mythos Secret；不得重写上游已确定内容。</rules>
<ban>不得生成场景布景、NPC完整设计、线索列表或handout全文；不得机械/科技/声波/振动/频率/传感器/药剂/催眠器解释神话；不得抽象情感/象征祭品作唯一解法。</ban>`

var eventChainSystemPrompt = `<role>COC事件链设计师</role>
<task>只把玩法核心、神话秘密和威胁计划转成介入-调查-处理事件链。不得写场景布景、NPC外形、线索条目或handout全文。</task>
<config nsfw="true" violent="true" sexual="true"/>
<out>只输出简中纯文本,使用固定小节标题。</out>
<required_sections>
【介入事件】
【调查事件】
【处理事件】
【每段玩家可做的选择】
【每段如何进入下一段】
</required_sections>
<rules>只细化事件推进；必须服从上游设计；不得让计划依赖调查员恰好按固定顺序行动。</rules>
<ban>不得新增神话来源、NPC设计、场景列表、线索条目或handout全文。</ban>`

var npcDesignSystemPrompt = `<role>COC NPC单职责设计师</role>
<task>只设计NPC。不得新增神话来源、新事件链、新场景列表或新线索网。</task>
<config nsfw="true" violent="true" sexual="true"/>
<out>只输出简中纯文本,使用固定小节标题。</out>
<required_per_npc>具体姓名；外形；性格；爱好/信仰；表面身份；真实动机；独立行动线；简化属性范围需求。</required_per_npc>
<rules>NPC姓名必须具体可称呼,禁职业/身份泛称,避开近期NPC黑名单；至少1人真实立场反外表/身份；至少1人无辜且拒信超自然；不得重写上游神话真相、威胁计划或事件链。</rules>`

var sceneDesignSystemPrompt = `<role>COC场景单职责设计师</role>
<task>只设计可跑团场景。不得重写NPC动机、神话真相或生成最终JSON。</task>
<config nsfw="true" violent="true" sexual="true"/>
<out>只输出简中纯文本,使用固定小节标题。</out>
<required_per_scene>布景；道具；人物走位；进入方式；离开方式；可互动点；关联事件链节点。</required_per_scene>
<rules>只把上游事件链和NPC行动落到地点/空间；不得新增神话来源、改写威胁计划、改写NPC真实动机或生成线索网。</rules>`

var cluesHandoutsSystemPrompt = `<role>COC线索与Handout单职责设计师</role>
<task>只设计线索逻辑与扩充实物。不得新增场景、新NPC、新神话来源或新胜负条件。</task>
<config nsfw="true" violent="true" sexual="true"/>
<out>只输出简中纯文本,使用固定小节标题。</out>
<required_sections>
【关键真相信息列表】
【每个关键信息的2-3条取得路径】
【[真实]/[隐藏]/[误导]线索草案】
【Handouts】报纸、信件、地图、照片、账本、族谱、病历、票据、录音等；每个说明取得地点、提供信息、是否关键。
</required_sections>
<rules>关键真相不得只有唯一线索瓶颈；handouts 不新增 schema 字段,后续只能写入 scenes[].description、clues[]、map_description。</rules>
<ban>不得新增神话来源、场景、NPC或胜负条件；不得用机械/科技/声波/振动/频率/传感器/药剂/催眠器解释神话；不得用抽象情感/象征祭品作唯一解法。</ban>`

// draftPrompt has 3 format args: design artifacts, scenarioExample, lengthSpec
const draftPrompt = `<task>将已锁定设计工件编码为完整JSON模组；严格遵循示例结构,不新增schema字段,不新增剧情事实。</task>
<config nsfw="true" violent="true" sexual="true"/>
<design_artifacts>
%s
</design_artifacts>
<json_example>
%s
</json_example>
<out>仅输出JSON,无其他文字。</out>
<fields>
- 只能编码 design_artifacts 中已经存在的设计,不得新增神话来源、NPC、场景、线索、反派步骤、胜负条件或新剧情事实。
- handouts 不新增 schema 字段,只能写入 scenes[].description、clues[]、map_description。
- system_prompt: KP指导2-3句。
- setting: 只写开局公开时代/地点/日常背景/气氛/社会常识；禁幕后真相、怪物/神话实体、仪式、隐藏身份、反转、胜负条件、后续剧情；自然像KP桌边介绍,非设定集/梗概。
- intro: 只写玩家可听的开场；第二人称；眼前处境、表面任务、委托公开信息、可立即行动目标；禁暗示真凶/超自然来源/隐藏线索/结局/“背后隐藏着”；具体简洁有现场感。
%s
- game_start_slot: 0-47,每槽30分钟；按剧情选。
- map_description: 简洁文字地图；主要地点、空间关系、移动路径；辅助KP定位；可简要列出公开地图或实物资料,但不得剧透隐藏真相。
- scenes: 每个 description 必须包含布景、道具、人物走位、进入方式、离开方式、可互动点；如该场景可取得handout,在description内明确“可取得实物/handout: 名称”。
- npcs: name/description/attitude/stats；name具体可称呼,禁近期黑名单/职业身份泛称；description/attitude必须包含外形、性格、爱好/信仰、真实动机、行动线,不要写成纯工具人。
- clues: 前缀格式 "[真实]名(地点):描述" / "[误导]名(地点):描述" / "[隐藏]名(地点):描述(需XXX检定)"；handout类线索必须写入 clues,格式如 "[真实]报纸剪报(镇图书馆):..."、"[隐藏]录音带(医生办公室):...(需聆音/图书馆利用检定)"。
- 必须至少包含1个handout/扩充实物类线索,例如报纸、信件、地图、录音、照片、账本、族谱、病历、票据；不得新增handouts字段。
- 关键真相不得只有唯一线索瓶颈；每个关键信息至少在 clues 或 scenes 中有2条路径可获得,包含现场勘查、询问NPC、解读文件/实物等替代路径。
- win_condition/lose_condition: 明确；partial_wins:1-3项。
- 永久/长期奖励须写入 scenes/clues/win_condition/partial_wins 的可执行路径,含地点、条件、风险、代价、后果；高风险高收益,禁背景一笔带过。</fields>
<ban>科学术语；用机械装置/电子设备/声波/振动/频率/传感器/药剂/催眠器/信号标记/安全区解释神话现象；用抽象情感/象征祭品(人性温度/珍贵之物/爱/记忆/牺牲/信念)作锚点/钥匙/封印/唯一解法。</ban>
`

var qaSystemPrompt = `<role>COC模组QA</role>
<task>审查可玩性、一致性、规则合规。</task>
<config nsfw="true" violent="true" sexual="true"/>
<rulebook>` + rulebook.RulebookDir + `</rulebook>
<tools>
search:{"action":"search","query":"自然语言规则查询"}
read_rulebook_const:{"action":"read_rulebook_const","constant":"rulebook_dir|rulebook_detail_dir|aliens|books|great_old_ones_and_gods|monsters|mythos_creatures|spells"}
response:{"action":"response","result":{"score":N,"pass":bool,"strengths":[...],"issues":[...],"must_fix":[...]}}
</tools>
<exec>
- 只允许输出单个JSON数组,数组元素只能是 search/read_rulebook_const/response 三类之一。
- 禁止输出 yield/think/comment/markdown/自然语言/空数组/空对象/缺少 query 的 search/缺少 constant 的 read_rulebook_const。
- 第1轮必须输出至少1个 read_rulebook_const 和至少1个 search,用于核实草案中的怪物/法术/技能/神话来源；第1轮禁止 response。
- 查询轮示例:[{"action":"read_rulebook_const","constant":"monsters"},{"action":"search","query":"深潜者 COC7 属性与行为"}]
- 收到规则书搜索结果后,若信息足够,必须只输出1个 response action；response 不得和 search/read_rulebook_const 混用。
- response轮示例:[{"action":"response","result":{"score":85,"pass":true,"strengths":["结构完整"],"issues":[],"must_fix":[]}}]
- 若信息仍不足,继续输出至少1个有效 search/read_rulebook_const；任何轮次都不得输出空查询或等待动作。</exec>
<score total="100">
结构20: 场景/NPC/线索/胜负齐全,lose/partial有意义,有介入事件/调查事件/处理事件链。
线索15: 含[真实]/[隐藏],有冗余路径,至少有1个handout/扩充实物类线索。
规则15: 神话元素来自规则书,NPC属性合规。
可玩15: 有明确核心玩法和真实决策,胜负依赖玩家行为非固定剧情。
文本5: setting/intro自然可读、无剧透、仅开局公开信息。
新颖15: 有意外设计；怪物登场用伪装/错位/环境/反转之一；避套路。
悬疑15: 未知与转折；恐惧来自认知失调/未知,非gore/伪科学。
加权: 忠实反映完整 design_artifacts；不得重写上游已确定的设定种子、神话来源、玩法核心、神话真相、威胁计划、事件链、NPC动机、场景职责或线索逻辑；奖励有条件/来源/风险/后果,不得因永久奖励扣分。</score>
<must_fix>
- 缺 lose_condition 或 partial_wins；缺[隐藏]线索；NPC全知情者；NPC名复用黑名单或职业泛称。
- 缺少核心玩法,或核心玩法不能落地为调查员行动循环/选择/处理方式。
- 缺少介入事件/调查事件/处理事件链,或事件链只靠固定剧情推进而非玩家行动。
- 反派/威胁计划没有明确目的、步骤、材料/地点/参与者、时限、失败后果或成功后果。
- 场景 description 缺少布景、道具、人物走位、进入方式、离开方式或可互动点,导致KP无法直接跑。
- 关键线索存在唯一瓶颈；关键真相没有2条以上获得路径；没有非运气推理胜利路径。
- 没有任何handout/扩充实物类线索(报纸、信件、地图、录音、照片、账本、族谱、病历、票据等),或handout没有取得地点/提供信息。
- NPC全是工具人,缺少外形、性格、信仰/爱好、真实动机或独立行动线。
- 三幕剧套壳无转折；怪物/神话不符规则书；怪物直白冲出/黑暗出现且无新颖手法。
- 无叙事功能gore；因果断链；NPC无动机；背离 design_artifacts 中的上游设计。
- 伪科学/科学术语解释神话；用机械装置/电子设备/声波/振动/频率/传感器/药剂/催眠器/信号标记/安全区解释神话现象；用抽象情感/象征祭品(人性温度/珍贵之物/爱/记忆/牺牲/信念)作锚点/钥匙/封印/唯一解法；空泛神秘描述。
- setting/intro 剧透幕后真相、怪物/实体、仪式、隐藏身份、反转、胜负或后续；或像设定集/梗概/模板。</must_fix>
<pass>score>=80 且 must_fix为空。</pass>`

const revisionPrompt = `<task>根据QA must_fix 对模组JSON做针对性修订。只修订JSON,不得重写上游设计工件或新增剧情事实。</task>
<out>仅输出修订后的完整JSON,无其他文字。</out>
<config nsfw="true" violent="true" sexual="true"/>
<design_artifacts>
%s
</design_artifacts>
<draft>
%s
</draft>
<must_fix>
%s
</must_fix>
<json_example>
%s
</json_example>`

// qaGuardResultExample is used as schema hint when parser LLM repairs QA result JSON.
const qaGuardResultExample = `{"score": 85, "pass": true, "strengths": ["优点1", "优点2"], "issues": ["问题1"], "must_fix": []}`

// scenarioExample is the anonymised lonely_island.json used as a structural reference.
const scenarioExample = `{
  "name": "示例模组名",
  "description": "模组简介",
  "author": "agent-team",
  "tags": "标签1,标签2",
  "min_players": 1,
  "max_players": 4,
  "difficulty": "normal",
  "content": {
    "system_prompt": "你是本场COC跑团的主持人(KP),你将主持名为《模组名》的剧本。保持克苏鲁宇宙恐怖的风格,营造神秘、压抑的氛围。",
    "game_start_slot": 16,
    "setting": "1923年，某地。只写玩家开局能知道的公开背景、当地气氛和表面状况，不透露幕后真相……",
    "intro": "你们抵达某地时，委托人只告诉你们一件表面上说得通的麻烦事。接下来要做什么很清楚，但真正原因还无人知晓……",
    "map_description": "【文字地图】\n主要地点及空间关系:\n- 地点A(起点):描述,与地点B相邻,步行约5分钟\n- 地点B:描述,位于地点A东侧,与地点C有小路相连\n- 地点C(终点/BOSS所在):描述,地处偏僻,需经过地点B才能抵达\n关键路径:A→B→C；隐秘路径:A→(密道)→C",
    "scenes": [
      {"id": "arrival", "name": "场景名称", "description": "场景描述", "triggers": ["start"]},
      {"id": "explore", "name": "场景名称", "description": "场景描述", "triggers": ["arrived"]},
      {"id": "climax", "name": "场景名称", "description": "场景描述", "triggers": ["clue_found"]}
    ],
    "npcs": [
      {
        "name": "NPC名",
        "description": "年龄、外貌、身份背景描述, 法术表(如有)",
        "attitude": "对调查员的态度和行为模式",
        "stats": {"STR": 60, "CON": 65, "SIZ": 55, "DEX": 50, "APP": 40, "INT": 70, "POW": 75, "EDU": 80, "HP": 12, "MP": 15}
      }
    ],
    "clues": [
      "[真实]线索名(发现地点):线索详细描述",
      "[真实]线索名(发现地点):线索详细描述（备用路径）",
      "[误导]线索名(发现地点):表面合理但指向错误结论的描述",
      "[隐藏]线索名(发现地点):线索详细描述（需图书馆利用/侦察/心理学检定）"
    ],
    "win_condition": "明确的胜利条件描述",
    "lose_condition": "明确的失败条件描述（如仪式在第X回合完成、关键NPC死亡等）",
    "partial_wins": [
      "部分胜利情景1：调查员阻止了仪式但BOSS逃脱",
      "部分胜利情景2：消灭了BOSS但神话知识已经泄露给了公众"
    ]
  }
}`

// ---------------------------------------------------------------------------
// Tool-call types for rulebook text and QA phases
// ---------------------------------------------------------------------------

type pipelineToolCall struct {
	Action   string         `json:"action"`
	Keyword  string         `json:"keyword,omitempty"`  // grep (kept for backward compat)
	Query    string         `json:"query,omitempty"`    // search
	Constant string         `json:"constant,omitempty"` // read_rulebook_const
	Text     string         `json:"text,omitempty"`     // response (single-responsibility text stages)
	Brief    string         `json:"brief,omitempty"`    // response (legacy story phase)
	Outline  string         `json:"outline,omitempty"`  // response (legacy outline phase)
	Result   *qaGuardResult `json:"result,omitempty"`   // response (QA phase)
}

// ---------------------------------------------------------------------------
// Types (kept from original)
// ---------------------------------------------------------------------------

type ScenarioCreationRequest struct {
	Name         string `json:"name"`
	Theme        string `json:"theme"`
	Era          string `json:"era"`
	Difficulty   string `json:"difficulty"`
	MinPlayers   int    `json:"min_players"`
	MaxPlayers   int    `json:"max_players"`
	TargetLength string `json:"target_length"`
	Brief        string `json:"brief"`
	Salt         string `json:"salt"`
}

type ScenarioCreationOutput struct {
	Draft      ScenarioDraft `json:"draft"`
	QA         qaGuardResult `json:"qa"`
	Iterations int           `json:"iterations"`
}

type qaGuardResult struct {
	Score     int      `json:"score"`
	Pass      bool     `json:"pass"`
	Strengths []string `json:"strengths"`
	Issues    []string `json:"issues"`
	MustFix   []string `json:"must_fix"`
}

type ScenarioDraft struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Author      string                 `json:"author"`
	Tags        string                 `json:"tags"`
	MinPlayers  int                    `json:"min_players"`
	MaxPlayers  int                    `json:"max_players"`
	Difficulty  string                 `json:"difficulty"`
	Content     models.ScenarioContent `json:"content"`
}

type scenarioDesignArtifacts struct {
	SettingSeed      string
	MythosSource     string
	GameplayCore     string
	MythosSecret     string
	ThreatPlan       string
	EventChain       string
	NPCDesign        string
	SceneDesign      string
	CluesAndHandouts string
}

// ---------------------------------------------------------------------------
// Entry point: 3-phase pipeline
// ---------------------------------------------------------------------------

func randomEra() string {
	eras := []string{"1920s", "1990s", "现代"}
	return eras[rand.Intn(len(eras))]
}

// narrativeTemplates 叙事结构模板，随机注入大纲 prompt，打破三幕剧单一套路。
var narrativeTemplates = []string{
	"非线性结构：多条独立调查线并行，玩家可自由选择探索顺序，最终汇聚至核心真相",
	"限时压迫：存在明确的倒计时（如天文现象、仪式日期），若干回合内未完成则神话降临，Bad End 自动触发",
	"信息不对称：每位调查员仅掌握部分碎片信息，必须在互信与保密间做出选择",
	"三幕经典结构：序章-调查-高潮",
	"完全开放：没有预设场景或线索，玩家的每个行动都可能引发新的事件和线索，故事由玩家行为驱动",
	"罗生门结构：不同NPC对同一事件给出互相矛盾但各自合理的证词，真相需从矛盾处拼合",
	"地点剥洋葱：围绕一个核心地点反复深入，每次返回都会揭开新的空间层级、历史痕迹或神话污染",
	"护送结构：调查员必须保护一名关键NPC或危险物件穿越多个地点，途中逐渐理解其真实价值和风险",
	"资源枯竭：时间、光源、交通、信誉或理智资源逐步减少，迫使玩家在调查深度与安全撤离间取舍",
	"追踪与反追踪：调查员寻找目标的同时也被神话势力或其代理人追踪，线索推进会改变敌方行动",
	"公开事件暗线：表面是公开社会事件，调查员在记者、警察、民众或地方势力注视下寻找隐藏的神话因果",
	"递归线索：每条线索既回答一个问题又引出更深问题，最终指向同一神话源头而非单一Boss战",
}

// topicThreatOrigins 威胁来源维度
var topicThreatOrigins = []string{
	"腐化的人类组织（邪教、秘密学会、政府黑计划）",
	"传统怪物(狼人、吸血鬼等经典恐怖生物)",
	"神话法师（人类但掌握了强大且危险的神话知识和法术）",
	"博学的神话法师（人类但对神话有深入研究，掌握丰富的神话知识和实际法术能力）",
	"旧日支配者的眷族（忠诚的仆从，可能是人类、怪物或混血种）",
	"沉睡已久的神话生物（即将苏醒或被意外唤醒）",
	"潜伏在人类社会中的神话生物（伪装成普通人或动物）",
	"外来的神话生物（从其他维度或星球入侵）",
	"伟大存在的直接干预（亲自降临或通过代理人直接影响世界）",
	"被神话典籍误导的人类研究者",
	"古老遗物的持续影响（规则书来源的神话物品正在吸引眷族或改变持有者）",
	"地下或水下的非人群落（与人类社区长期共存，因边界被破坏而冲突升级）",
	"血脉污染或混血传承（某个家族、社群或职业团体逐渐显露非人特征）",
	"死者或失踪者的异常回归（回来的不是原本人类，或带回了神话世界的规则）",
}

// topicTwists NPC/剧情转折维度
var topicTwists = []string{
	"雇主本身是幕后黑手",
	"BOSS实为受害者，真正的威胁尚未露面",
	"渐进式，威胁逐步升级",
	"关键NPC一直在帮助，但其动机本身构成最终危机",
	"所有人以为的‘超自然现象’实为精心策划的人类阴谋",
	"本应最可靠的盟友阵营里，隐藏着唯一知道真相的叛逃者",
	"怪物并非被制造出来，而是在逃离比它更可怕的存在",
	"故事发生的世界本身是一个梦境",
	"最初的受害者其实是主动召来神话影响的人，但后来失去控制",
	"调查员要阻止的仪式其实已经失败，真正危险来自失败后的副作用",
	"地方权威一直在掩盖真相，但其目的不是作恶而是拖延更大灾难",
	"关键证据是真的，但被放在错误语境中会得出完全相反的结论",
	"看似无害的日常职业或地方习俗，实际是维持封印/边界的残缺仪式",
}

var gameplay = []string{
	"大逃杀，逃离事件发生地点",
	"与恋爱、性有关的18X、NSFW内容",
	"智力竞赛，侧重解谜和策略",
	"大世界探索, 在开放世界中自由探索, 线索分布在各个角落",
	"密室逃脱，限时破解封闭空间内的连环谜题",
	"恐怖生存，在压抑环境中管理理智与资源，躲避不可名状的威胁",
	"阵营对抗，玩家分属不同秘密阵营，通过欺诈与合作达成各自目标",
	"间谍潜入，伪装身份渗透目标组织，窃取情报并安全撤离",
	"地城探险，深入随机生成的地下城，战胜怪物收集战利品",
	"都市怪谈，调查现代都市中的异常现象，揭开传闻背后的真相",
	"末日重建，在灾后世界带领幸存者建立据点，平衡资源与人性",
	"物品收集，寻找并组合散落世界各地的神话物品，解锁隐藏剧情和能力",
	"调查沙盒，多个地点可自由访问，线索按主题而非固定顺序组织",
	"社会潜入，利用身份、关系和话术进入封闭社群或组织核心",
	"追踪狩猎，沿着踪迹、目击记录和异常痕迹寻找移动中的神话目标",
	"据点防守，在有限资源下保护安全屋、避难所或关键证人直到真相揭露",
	"航程/旅途结构，交通工具或迁徙路线上的每一站都揭开一层真相",
	"拍卖/交易会结构，多方势力争夺同一神话物品，玩家可谈判、盗取或毁弃",
	"档案考据，通过旧报纸、族谱、地产记录、审判卷宗等拼合历史真相",
	"追逐逃亡，调查员掌握关键证据后被追杀，必须边逃边验证真相",
	"仪式干预，玩家不只是阻止仪式，也可改变地点、材料、参与者或时机来获得部分胜利",
	"怪物谈判，非人存在有可理解但危险的目标，玩家可沟通、误导、交易或驱逐",
}

func randomNarrativeTemplate() string {
	return narrativeTemplates[rand.Intn(len(narrativeTemplates))]
}

func randomTopicConstraints(threatNum int) string {
	if threatNum > len(topicThreatOrigins) {
		threatNum = len(topicThreatOrigins)
	}
	threats := make([]string, len(topicThreatOrigins))
	copy(threats, topicThreatOrigins)
	rand.Shuffle(len(threats), func(i, j int) { threats[i], threats[j] = threats[j], threats[i] })
	threats = threats[:threatNum]
	if rand.Intn(10) == 1 {
		threats = append(threats, "委托人是奈亚拉托提普的化身(不表现为敌对,利用调查员达成自己的不可告人目的,在结尾揭开真相并嘲笑调查员的愚蠢; 注: 请自由设定化身的形象不必局限在经典形象, 但必须是人形且在故事中一直以人类身份出现)")
	}
	twist := topicTwists[rand.Intn(len(topicTwists))]
	gamePlay := gameplay[rand.Intn(len(gameplay))]
	return fmt.Sprintf("威胁来源=%v | 核心转折=%s | 游戏玩法=%s", threats, twist, gamePlay)
}

var genScenarioMutex sync.Mutex

func RunScripterScenarioTeam(ctx context.Context, req ScenarioCreationRequest) (ScenarioCreationOutput, error) {
	genScenarioMutex.Lock()
	defer genScenarioMutex.Unlock()

	if req.MinPlayers <= 0 {
		req.MinPlayers = 1
	}
	if req.MaxPlayers <= 0 {
		req.MaxPlayers = 4
	}
	if req.Difficulty == "" {
		req.Difficulty = "normal"
	}
	if req.Era == "" {
		req.Era = randomEra()
	}
	if req.Theme == "" {
		num := 1
		if req.Difficulty == "hard" {
			num = 3
		} else if req.Difficulty == "normal" {
			num = 2
		}
		req.Theme = randomTopicConstraints(num)
		monsterNum := 1
		if req.Difficulty == "hard" {
			monsterNum = 5
		} else if req.Difficulty == "normal" {
			monsterNum = 3
		}
		req.Theme += " | 主要怪物种类=" + fmt.Sprint(monsterNum)
	}

	reqJSON, _ := json.Marshal(req)
	log.Printf("[scripter] 开始单职责流水线生成 req=%s", reqJSON)
	debugf("script", "theme: %v", req.Theme)

	architect, err := loadSingleAgent(models.AgentRoleArchitect)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	qaAgent, err := loadSingleAgent(models.AgentRoleQAGuard)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	parser, err := loadSingleAgent(models.AgentRoleParser)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}

	npcNameBlacklist := loadRecentNPCNameBlacklist(200)
	debugf("script", "npc blacklist count: %d", len(npcNameBlacklist))

	var artifacts scenarioDesignArtifacts
	artifacts.SettingSeed, err = generateSettingSeed(ctx, architect, req)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("setting seed 生成失败: %w", err)
	}
	log.Printf("[setting] len=%d", len([]rune(artifacts.SettingSeed)))

	artifacts.MythosSource, err = selectMythosSource(ctx, architect, req, artifacts)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("mythos source 生成失败: %w", err)
	}
	log.Printf("[mythos] len=%d", len([]rune(artifacts.MythosSource)))

	artifacts.GameplayCore, err = designGameplayCore(ctx, architect, req, artifacts)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("gameplay core 生成失败: %w", err)
	}
	log.Printf("[core] len=%d", len([]rune(artifacts.GameplayCore)))

	artifacts.MythosSecret, err = designMythosSecret(ctx, architect, req, artifacts)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("mythos secret 生成失败: %w", err)
	}
	log.Printf("[secret] len=%d", len([]rune(artifacts.MythosSecret)))

	artifacts.ThreatPlan, err = designThreatPlan(ctx, architect, req, artifacts)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("threat plan 生成失败: %w", err)
	}
	log.Printf("[threat] len=%d", len([]rune(artifacts.ThreatPlan)))

	artifacts.EventChain, err = designEventChain(ctx, architect, req, artifacts)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("event chain 生成失败: %w", err)
	}
	log.Printf("[events] len=%d", len([]rune(artifacts.EventChain)))

	artifacts.NPCDesign, err = designNPCs(ctx, architect, req, artifacts, npcNameBlacklist)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("NPC design 生成失败: %w", err)
	}
	log.Printf("[npcs] len=%d", len([]rune(artifacts.NPCDesign)))

	artifacts.SceneDesign, err = designScenes(ctx, architect, req, artifacts)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("scene design 生成失败: %w", err)
	}
	log.Printf("[scenes] len=%d", len([]rune(artifacts.SceneDesign)))

	artifacts.CluesAndHandouts, err = designCluesAndHandouts(ctx, architect, req, artifacts)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("clues and handouts 生成失败: %w", err)
	}
	log.Printf("[clues] len=%d", len([]rune(artifacts.CluesAndHandouts)))

	designSource := renderDesignArtifacts(artifacts)
	debugf("script", "design artifacts: %v", designSource)

	draft, err := buildDraft(ctx, architect, parser, designSource, req.TargetLength, npcNameBlacklist)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("draft 失败: %w", err)
	}
	applyGuardrails(&draft, req)
	log.Printf("[draft] name=%q scenes=%d npcs=%d clues=%d",
		draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))

	var qaResult qaGuardResult
	for i := 0; i < 30; i++ {
		if ctx.Err() != nil {
			return ScenarioCreationOutput{}, ctx.Err()
		}
		qaResult, err = runQA(ctx, qaAgent, parser, req, designSource, draft, npcNameBlacklist)
		if err != nil {
			log.Printf("[qa] failed iter=%d: %v", i, err)
			return ScenarioCreationOutput{}, fmt.Errorf("QA 失败: %w", err)
		}
		log.Printf("[qa] iter=%d score=%d pass=%v must_fix=%d",
			i, qaResult.Score, qaResult.Pass, len(qaResult.MustFix))

		if qaResult.Pass {
			return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: i + 1}, nil
		}
		if i == 2 {
			break
		}

		revised, revErr := reviseDraft(ctx, architect, parser, draft, qaResult.MustFix, designSource, npcNameBlacklist)
		if revErr != nil {
			log.Printf("[qa] revision failed iter=%d: %v", i, revErr)
			break
		}
		applyGuardrails(&revised, req)
		draft = revised
		log.Printf("[qa] revision iter=%d done", i)
	}

	return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: 3}, nil
}

// ---------------------------------------------------------------------------
// Phase 1: Single-responsibility design stages
// ---------------------------------------------------------------------------

func generateSettingSeed(ctx context.Context, architect agentHandle, req ScenarioCreationRequest) (string, error) {
	reqJSON, _ := json.Marshal(req)
	prompt := fmt.Sprintf(`请只完成 Setting Seed 阶段,锁定基础可跑团元素。不要写神话、怪物、仪式、黑暗真相、反派计划或结局。

<request_json>
%s
</request_json>`, string(reqJSON))
	return runTextStage(ctx, architect, "setting", settingSeedSystemPrompt, prompt)
}

func selectMythosSource(ctx context.Context, architect agentHandle, req ScenarioCreationRequest, artifacts scenarioDesignArtifacts) (string, error) {
	reqJSON, _ := json.Marshal(req)
	prompt := fmt.Sprintf(`请只完成 Mythos Source 阶段:先查规则书,再锁定一个可用神话来源。不得写故事、NPC、场景、线索或结局。

<request_json>
%s
</request_json>

<locked_setting_seed>
%s
</locked_setting_seed>

<candidate_hint>
%s
</candidate_hint>`, string(reqJSON), artifacts.SettingSeed, mythosCandidateHint(req))
	return runRulebookTextStage(ctx, architect, "mythos", mythosSourceSystemPrompt, prompt)
}

func designGameplayCore(ctx context.Context, architect agentHandle, req ScenarioCreationRequest, artifacts scenarioDesignArtifacts) (string, error) {
	reqJSON, _ := json.Marshal(req)
	template := randomNarrativeTemplate()
	log.Printf("[core] 叙事模板: %s", template)
	prompt := fmt.Sprintf(`请只完成 Gameplay Core 阶段。只确定玩家体验和核心玩法,不得写黑暗真相细节、反派步骤、NPC细节、场景细节或线索列表。

<request_json>
%s
</request_json>

<narrative_template>
%s
</narrative_template>

<locked_artifacts>
%s
</locked_artifacts>`, string(reqJSON), template, renderDesignArtifacts(artifacts))
	return runTextStage(ctx, architect, "core", gameplayCoreSystemPrompt, prompt)
}

func designMythosSecret(ctx context.Context, architect agentHandle, req ScenarioCreationRequest, artifacts scenarioDesignArtifacts) (string, error) {
	reqJSON, _ := json.Marshal(req)
	prompt := fmt.Sprintf(`请只完成 Mythos Secret 阶段。把已锁定规则书神话来源转成幕后真相；不得写反派计划步骤、NPC列表、场景列表或线索网。

<request_json>
%s
</request_json>

<locked_artifacts>
%s
</locked_artifacts>`, string(reqJSON), renderDesignArtifacts(artifacts))
	return runTextStage(ctx, architect, "secret", mythosSecretSystemPrompt, prompt)
}

func designThreatPlan(ctx context.Context, architect agentHandle, req ScenarioCreationRequest, artifacts scenarioDesignArtifacts) (string, error) {
	reqJSON, _ := json.Marshal(req)
	prompt := fmt.Sprintf(`请只完成 Threat Plan 阶段。只确定反派/威胁计划；不得设计场景、完整NPC或线索。

<request_json>
%s
</request_json>

<locked_artifacts>
%s
</locked_artifacts>`, string(reqJSON), renderDesignArtifacts(artifacts))
	return runTextStage(ctx, architect, "threat", threatPlanSystemPrompt, prompt)
}

func designEventChain(ctx context.Context, architect agentHandle, req ScenarioCreationRequest, artifacts scenarioDesignArtifacts) (string, error) {
	reqJSON, _ := json.Marshal(req)
	prompt := fmt.Sprintf(`请只完成 Event Chain 阶段。把玩法核心、神话秘密和威胁计划转成事件链；不得写场景布景、NPC外形、线索条目或handout全文。

<request_json>
%s
</request_json>

<locked_artifacts>
%s
</locked_artifacts>`, string(reqJSON), renderDesignArtifacts(artifacts))
	return runTextStage(ctx, architect, "events", eventChainSystemPrompt, prompt)
}

func designNPCs(ctx context.Context, architect agentHandle, req ScenarioCreationRequest, artifacts scenarioDesignArtifacts, npcNameBlacklist []string) (string, error) {
	reqJSON, _ := json.Marshal(req)
	prompt := fmt.Sprintf(`请只完成 NPC Design 阶段。只设计NPC；不得新增神话来源、新事件链、新场景列表或新线索网。

<request_json>
%s
</request_json>

<locked_artifacts>
%s
</locked_artifacts>

<recent_npc_name_blacklist>
%s
</recent_npc_name_blacklist>`, string(reqJSON), renderDesignArtifacts(artifacts), formatNPCNameBlacklist(npcNameBlacklist))
	return runTextStage(ctx, architect, "npcs", npcDesignSystemPrompt, prompt)
}

func designScenes(ctx context.Context, architect agentHandle, req ScenarioCreationRequest, artifacts scenarioDesignArtifacts) (string, error) {
	reqJSON, _ := json.Marshal(req)
	prompt := fmt.Sprintf(`请只完成 Scene Design 阶段。只设计可跑团场景；不得重写NPC动机、神话真相或生成最终JSON。

<request_json>
%s
</request_json>

<locked_artifacts>
%s
</locked_artifacts>`, string(reqJSON), renderDesignArtifacts(artifacts))
	return runTextStage(ctx, architect, "scenes", sceneDesignSystemPrompt, prompt)
}

func designCluesAndHandouts(ctx context.Context, architect agentHandle, req ScenarioCreationRequest, artifacts scenarioDesignArtifacts) (string, error) {
	reqJSON, _ := json.Marshal(req)
	prompt := fmt.Sprintf(`请只完成 Clues & Handouts 阶段。只设计线索逻辑与扩充实物；不得新增场景、新NPC、新神话来源或新胜负条件。

<request_json>
%s
</request_json>

<locked_artifacts>
%s
</locked_artifacts>`, string(reqJSON), renderDesignArtifacts(artifacts))
	return runTextStage(ctx, architect, "clues", cluesHandoutsSystemPrompt, prompt)
}

func mythosCandidateHint(req ScenarioCreationRequest) string {
	num := 2
	if req.Difficulty == "hard" {
		num = 4
	} else if req.Difficulty == "normal" {
		num = 3
	}
	return fmt.Sprintf("神祇候选=%v\n神话生物候选=%v\n怪物候选=%v\n典籍候选=%v\n法术候选=%v",
		shuffledSample(rulebook.GreadOldOnesAndGods, num),
		shuffledSample(rulebook.MythosCreatures, num),
		shuffledSample(rulebook.Monsters, num),
		shuffledSample(rulebook.Books, num),
		shuffledSample(rulebook.Spells, num))
}

func shuffledSample(values []string, n int) []string {
	if len(values) == 0 || n <= 0 {
		return nil
	}
	items := append([]string(nil), values...)
	rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
	if n > len(items) {
		n = len(items)
	}
	return items[:n]
}

func runTextStage(ctx context.Context, agent agentHandle, tag, systemPrompt, userPrompt string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: agent.systemPrompt(systemPrompt)},
		{Role: "user", Content: userPrompt},
	}
	raw, err := agent.provider.Chat(ctx, msgs)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(llm.StripCodeFence(raw))
	if text == "" {
		return "", fmt.Errorf("%s 未生成文本", tag)
	}
	debugf(tag, "text: %v", text)
	return text, nil
}

func runRulebookTextStage(ctx context.Context, agent agentHandle, tag, systemPrompt, userPrompt string) (string, error) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: agent.systemPrompt(systemPrompt)},
		{Role: "user", Content: userPrompt},
	}
	const maxIter = 30
	readConstCount := 0
	searchCount := 0
	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		log.Printf("[%s] iter=%d", tag, iter+1)

		raw, err := agent.provider.Chat(ctx, msgs)
		if err != nil {
			return "", err
		}
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})
		debugf(tag, "raw: %v", raw)

		calls := parsePipelineCalls(ctx, raw)
		if len(calls) == 0 {
			if readConstCount > 0 && searchCount > 0 {
				text := strings.TrimSpace(llm.StripCodeFence(raw))
				if text != "" {
					return text, nil
				}
			}
			return "", fmt.Errorf("%s 未返回有效 tool call", tag)
		}

		filtered := make([]pipelineToolCall, 0, len(calls))
		for _, c := range calls {
			if c.Action == "yield" {
				continue
			}
			filtered = append(filtered, c)
		}
		calls = filtered

		for _, c := range calls {
			if c.Action != "response" {
				continue
			}
			text := strings.TrimSpace(toolCallText(c))
			if text == "" {
				continue
			}
			if readConstCount == 0 || searchCount == 0 {
				return "", fmt.Errorf("%s 在完成规则书 read_rulebook_const 和 search 前返回 response", tag)
			}
			log.Printf("[%s] iter=%d response 完成", tag, iter+1)
			return text, nil
		}

		for _, c := range calls {
			if c.Action == "read_rulebook_const" && strings.TrimSpace(c.Constant) != "" {
				readConstCount++
			}
			if c.Action == "search" && strings.TrimSpace(c.Query) != "" {
				searchCount++
			}
		}

		feedback := executeSearchCalls(ctx, calls, tag)
		if feedback == "" {
			return "", fmt.Errorf("%s 未返回有效查询 tool call", tag)
		}
		msgs = append(msgs, llm.ChatMessage{
			Role:    "user",
			Content: "规则书查询结果如下。它们是内部工具结果,不是新创作需求；请继续同一阶段职责,不得扩大职责或改写上游内容。\n\n" + feedback,
		})
	}
	return "", fmt.Errorf("%s 达到最大迭代仍未返回 response", tag)
}

func toolCallText(c pipelineToolCall) string {
	if strings.TrimSpace(c.Text) != "" {
		return strings.TrimSpace(c.Text)
	}
	if strings.TrimSpace(c.Brief) != "" {
		return strings.TrimSpace(c.Brief)
	}
	return strings.TrimSpace(c.Outline)
}

func renderDesignArtifacts(artifacts scenarioDesignArtifacts) string {
	sections := []struct {
		title string
		body  string
	}{
		{"Setting Seed", artifacts.SettingSeed},
		{"Mythos Source", artifacts.MythosSource},
		{"Gameplay Core", artifacts.GameplayCore},
		{"Mythos Secret", artifacts.MythosSecret},
		{"Threat Plan", artifacts.ThreatPlan},
		{"Event Chain", artifacts.EventChain},
		{"NPC Design", artifacts.NPCDesign},
		{"Scene Design", artifacts.SceneDesign},
		{"Clues & Handouts", artifacts.CluesAndHandouts},
	}
	var sb strings.Builder
	for _, section := range sections {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("## ")
		sb.WriteString(section.title)
		sb.WriteString("\n")
		body := strings.TrimSpace(section.body)
		if body == "" {
			body = "(pending)"
		}
		sb.WriteString(body)
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Phase 2: Build Draft (pure JSON, no tool calls)
// ---------------------------------------------------------------------------

func buildDraft(ctx context.Context, architect, fixer agentHandle, designSource string, targetLength string, npcNameBlacklist []string) (ScenarioDraft, error) {
	userMsg := fmt.Sprintf(draftPrompt, designSource, scenarioExample, lengthSpec(targetLength))
	userMsg += "\n\n【近期已用 NPC 名字黑名单，禁止 npcs[].name 复用】\n" + formatNPCNameBlacklist(npcNameBlacklist)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是 COC TRPG 模组 JSON 生成器。仅输出合法 JSON,不要有任何其他文字。"},
		{Role: "user", Content: userMsg},
	}

	var draft ScenarioDraft
	if err := chatAndParseDraft(ctx, architect, fixer, msgs, &draft); err != nil {
		return ScenarioDraft{}, err
	}
	return draft, nil
}

// ---------------------------------------------------------------------------
// Phase 3: QA (with tool-call loop for grep)
// ---------------------------------------------------------------------------

func runQA(ctx context.Context, qaAgent agentHandle, parser agentHandle, req ScenarioCreationRequest, designSource string, draft ScenarioDraft, npcNameBlacklist []string) (qaGuardResult, error) {
	reqJSON, _ := json.Marshal(req)
	draftJSON, _ := json.Marshal(draft)

	userMsg := fmt.Sprintf("审查以下 COC 模组JSON是否忠实反映 design_artifacts, 是否符合逻辑、可玩性和规则约束。QA只做审查,不得改写设计。\n\n【原始需求】\n%s\n\n【设计工件/唯一设计来源】\n%s\n\n【近期已用 NPC 名字黑名单，npcs[].name 禁止复用】\n%s\n\n【模组草案】\n%s",
		string(reqJSON), designSource, formatNPCNameBlacklist(npcNameBlacklist), string(draftJSON))

	msgs := []llm.ChatMessage{
		{Role: "system", Content: qaAgent.systemPrompt(qaSystemPrompt)},
		{Role: "user", Content: userMsg},
	}

	const maxIter = 30
	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return qaGuardResult{}, ctx.Err()
		}
		log.Printf("[qa] iter=%d", iter+1)

		raw, err := qaAgent.provider.Chat(ctx, msgs)
		if err != nil {
			return qaGuardResult{}, err
		}
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls := parsePipelineCalls(ctx, raw)
		if len(calls) == 0 {
			// Try direct JSON parse as fallback, use parser LLM on failure
			result, err := parseQAResultWithLLM(ctx, parser, raw)
			if err == nil {
				return result, nil
			}
			return qaGuardResult{}, fmt.Errorf("qa_guard 未返回可解析的 tool call 或 JSON, %v", err)
		}
		tmp := make([]pipelineToolCall, 0)
		for _, c := range calls {
			if c.Action != "yield" {
				tmp = append(tmp, c)
			}
		}
		calls = tmp

		// Check for response
		for _, c := range calls {
			if c.Action == "response" {
				if c.Result != nil {
					log.Printf("[qa] iter=%d response score=%d pass=%v", iter+1, c.Result.Score, c.Result.Pass)
					return *c.Result, nil
				}
				// result field failed to parse in pipelineToolCall — extract raw response JSON and repair
				log.Printf("[qa] iter=%d response c.Result==nil,尝试从原始输出解析", iter+1)
				result, repErr := parseQAResultWithLLM(ctx, parser, raw)
				if repErr != nil {
					return qaGuardResult{}, fmt.Errorf("qa result LLM修复失败: %w", repErr)
				}
				return result, nil
			}
		}

		// Execute search calls
		feedback := executeSearchCalls(ctx, calls, "qa")
		if feedback == "" {
			return qaGuardResult{}, fmt.Errorf("qa_guard 未返回有效 tool call")
		}
		msgs = append(msgs, llm.ChatMessage{
			Role:    "user",
			Content: "规则书搜索结果如下,请据此完成审查:\n\n" + feedback,
		})
	}

	return qaGuardResult{}, fmt.Errorf("qa_guard 达到最大迭代仍未返回 response")
}

// ---------------------------------------------------------------------------
// Revision: targeted fix based on QA feedback (pure JSON, no tool calls)
// ---------------------------------------------------------------------------

func reviseDraft(ctx context.Context, architect, fixer agentHandle, draft ScenarioDraft, mustFix []string, designSource string, npcNameBlacklist []string) (ScenarioDraft, error) {
	draftJSON, _ := json.Marshal(draft)
	issues := strings.Join(mustFix, "\n- ")

	userMsg := fmt.Sprintf(revisionPrompt, designSource, string(draftJSON), issues, scenarioExample)
	userMsg += "\n\n【近期已用 NPC 名字黑名单，修订后的 npcs[].name 禁止复用】\n" + formatNPCNameBlacklist(npcNameBlacklist)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是 COC TRPG 模组JSON修订器。只根据QA must_fix 修订JSON,必须忠实于design_artifacts,不得新增剧情事实。仅输出修订后的完整 JSON,不要有其他文字。"},
		{Role: "user", Content: userMsg},
	}

	var revised ScenarioDraft
	if err := chatAndParseDraft(ctx, architect, fixer, msgs, &revised); err != nil {
		return ScenarioDraft{}, err
	}
	return revised, nil
}

// ---------------------------------------------------------------------------
// Shared: parse tool calls & execute grep
// ---------------------------------------------------------------------------

var pipelineToolCallExample = func() string {
	calls := make([]pipelineToolCall, 1)
	data, _ := json.Marshal(calls)
	return string(data)
}()

func parsePipelineCalls(c context.Context, raw string) []pipelineToolCall {
	var calls []pipelineToolCall
	normalized := llm.StripCodeFence(strings.TrimSpace(raw))
	err := json.Unmarshal([]byte(normalized), &calls)
	if err == nil {
		return calls
	}
	if firstArray := extractFirstJSONArray(normalized); firstArray != "" {
		if arrayErr := json.Unmarshal([]byte(firstArray), &calls); arrayErr == nil {
			return calls
		}
	}

	raw = normalized
	const maxIter = 10
	for i := 0; i < maxIter; i++ {
		raw, err = RepairJSON(c, raw, err, pipelineToolCallExample)
		if err != nil {
			continue
		}
		raw = llm.StripCodeFence(strings.TrimSpace(raw))
		if firstArray := extractFirstJSONArray(raw); firstArray != "" {
			raw = firstArray
		}
		err = json.Unmarshal([]byte(raw), &calls)
		if err == nil {
			break
		}
	}
	return calls
}

func extractFirstJSONArray(raw string) string {
	start := -1
	depth := 0
	inString := false
	escaped := false
	for i, r := range raw {
		if start < 0 {
			if r == '[' {
				start = i
				depth = 1
			}
			continue
		}

		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch r {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch r {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				candidate := raw[start : i+1]
				if json.Valid([]byte(candidate)) {
					return candidate
				}
				return ""
			}
		}
	}
	return ""
}

func executeSearchCalls(ctx context.Context, calls []pipelineToolCall, tag string) string {
	var sb strings.Builder
	for _, c := range calls {
		switch c.Action {
		case "search":
			if c.Query == "" {
				continue
			}
			log.Printf("[%s] search query=%q", tag, c.Query)
			lawyerHandle, err := loadSingleAgent(models.AgentRoleLawyer)
			if err != nil {
				log.Printf("[%s] search: lawyer agent 加载失败: %v", tag, err)
				sb.WriteString(fmt.Sprintf("【search:%s】\n(lawyer agent 不可用)\n\n", c.Query))
				continue
			}
			results := runLawyer(ctx, lawyerHandle, c.Query, rulebook.GlobalIndex)
			sb.WriteString(fmt.Sprintf("【search:%s】\n%s\n\n", c.Query, formatLawyerResults(results)))
		case "read_rulebook_const":
			if c.Constant == "" {
				continue
			}
			text := rulebook.ReadConstant(c.Constant)
			sb.WriteString(fmt.Sprintf("【read_rulebook_const:%s】\n%s\n\n", c.Constant, text))
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// chatAndParseDraft calls the generator LLM once, then hands JSON repair to
// the parser agent when unmarshal fails.
func chatAndParseDraft(ctx context.Context, generator agentHandle, parser agentHandle, msgs []llm.ChatMessage, out *ScenarioDraft) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Step 1: generator produces the draft
	raw, err := generator.provider.Chat(ctx, msgs)
	if err != nil {
		return err
	}
	parseErr := parseJSONObject(raw, out)
	if parseErr == nil {
		return nil
	}
	log.Printf("[draft] generator JSON parse failed: %v", parseErr)

	// Step 2: parser agent repairs the JSON
	fixed, repairErr := repairJSONWith(ctx, parser, raw, parseErr, scenarioExample)
	if repairErr != nil {
		return fmt.Errorf("draft JSON 修复失败: %w (原始错误: %v)", repairErr, parseErr)
	}
	if err := parseJSONObject(fixed, out); err == nil {
		return nil
	} else {
		// First repair can return syntactically valid JSON but still mismatched schema.
		// Feed the concrete schema error back into parser once more.
		log.Printf("[draft] parser output schema mismatch, retry parser: %v", err)
		repairedAgain, repairErr2 := repairJSONWith(ctx, parser, fixed, err, scenarioExample)
		if repairErr2 != nil {
			return fmt.Errorf("修复后的 JSON 结构仍不匹配,二次修复失败: %w (结构错误: %v)", repairErr2, err)
		}
		if err2 := parseJSONObject(repairedAgain, out); err2 != nil {
			return fmt.Errorf("二次修复后的 JSON 仍无法解析为 ScenarioDraft: %w", err2)
		}
	}
	return nil
}

// RepairJSON uses the parser agent to fix malformed JSON. Exported so other
// subsystems (e.g. director) can reuse the same low-temperature fixer.
// rawJSON is the broken output, parseErr is the error from json.Unmarshal,
// schemaExample is a correct JSON example showing the expected structure.
// Returns the repaired JSON string, or an error if repair fails.
func RepairJSON(ctx context.Context, rawJSON string, parseErr error, schemaExample string) (string, error) {
	if strings.HasPrefix(rawJSON, "```json") {
		rawJSON = strings.TrimPrefix(rawJSON, "```json")
		rawJSON = strings.TrimSuffix(rawJSON, "```")
		return rawJSON, nil
	}
	isArray := strings.HasPrefix(schemaExample, "[") && strings.HasSuffix(schemaExample, "]")
	if isArray {
		fixed := false
		if !strings.HasPrefix(rawJSON, "[") {
			rawJSON = "[" + rawJSON
			fixed = true
		}
		if !strings.HasSuffix(rawJSON, "]") {
			rawJSON = rawJSON + "]"
			fixed = true
		}
		if fixed && json.Valid([]byte(rawJSON)) {
			debugf("repair", "fixed: %v", rawJSON)
			return rawJSON, nil
		}
	}
	parser, err := loadSingleAgent(models.AgentRoleParser)
	if err != nil {
		return "", fmt.Errorf("parser agent 未配置: %w", err)
	}
	return repairJSONWith(ctx, parser, rawJSON, parseErr, schemaExample)
}

func repairJSONWith(ctx context.Context, parser agentHandle, rawJSON string, parseErr error, schemaExample string) (string, error) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是 JSON 修复工具。用户会给你一段有问题的 JSON 和错误信息,你需要修复它使其匹配目标格式。仅输出修正后的合法 JSON,不要有任何其他文字。"},
	}

	const maxAttempts = 20
	currentErr := parseErr
	raw := rawJSON
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		fixPrompt := fmt.Sprintf(
			"以下 JSON 无法解析为目标结构。\n\n"+
				"【解析错误】\n%s\n\n"+
				"【原始 JSON】\n%s\n\n"+
				"【目标格式示例】\n%s\n\n"+
				"请修复并输出完整的合法 JSON。",
			currentErr.Error(), raw, schemaExample)
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fixPrompt})

		fixed, chatErr := parser.provider.Chat(ctx, msgs)
		if chatErr != nil {
			return "", fmt.Errorf("parser 调用失败: %w", chatErr)
		}
		if strings.HasPrefix(fixed, "```json") {
			fixed = strings.TrimPrefix(fixed, "```json")
			fixed = strings.TrimSuffix(fixed, "```")
		}
		debugf("Parser", "Fixed JSON: %v", fixed)

		// Verify the fix by stripping code fences
		stripped := llm.StripCodeFence(strings.TrimSpace(fixed))
		if json.Valid([]byte(stripped)) {
			log.Printf("[parser] JSON 修复成功 attempt=%d", attempt)
			return stripped, nil
		}
		// Extract {...} if surrounded by text
		if s := strings.Index(stripped, "{"); s >= 0 {
			if e := strings.LastIndex(stripped, "}"); e > s {
				candidate := stripped[s : e+1]
				if json.Valid([]byte(candidate)) {
					log.Printf("[parser] JSON 修复成功(提取) attempt=%d", attempt)
					return candidate, nil
				}
			}
		}

		currentErr = fmt.Errorf("修复后的 JSON 仍然无效")
		raw = fixed
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: fixed})
		log.Printf("[parser] attempt=%d 修复后仍无效", attempt)
	}
	return "", fmt.Errorf("parser 修复失败(%d次尝试)", maxAttempts)
}

// parseQAResultWithLLM tries direct JSON unmarshal of a qaGuardResult,
// falling back to parser LLM repair on failure.
func parseQAResultWithLLM(ctx context.Context, parser agentHandle, raw string) (qaGuardResult, error) {
	var result qaGuardResult
	if err := parseJSONObject(raw, &result); err == nil {
		return result, nil
	} else {
		log.Printf("[qa] 直接解析失败,使用parser LLM修复: %v", err)
		fixed, repairErr := repairJSONWith(ctx, parser, raw, err, qaGuardResultExample)
		if repairErr != nil {
			return qaGuardResult{}, fmt.Errorf("qa result JSON 修复失败: %w (原始错误: %v)", repairErr, err)
		}
		var result2 qaGuardResult
		if err2 := parseJSONObject(fixed, &result2); err2 != nil {
			return qaGuardResult{}, fmt.Errorf("修复后的 qa result 仍无法解析: %w", err2)
		}
		return result2, nil
	}
}

func parseJSONObject[T any](raw string, out *T) error {
	var err error
	stripped := llm.StripCodeFence(strings.TrimSpace(raw))
	if err = json.Unmarshal([]byte(stripped), out); err == nil {
		return nil
	}
	s := strings.Index(stripped, "{")
	e := strings.LastIndex(stripped, "}")
	if s >= 0 && e > s {
		if err = json.Unmarshal([]byte(stripped[s:e+1]), out); err == nil {
			return nil
		}
	}
	return fmt.Errorf("JSON 解析失败: %w", err)
}

func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// lengthSpec returns scene/clue count requirements based on target_length.
func lengthSpec(targetLength string) string {
	switch targetLength {
	case "long":
		return "- scenes: 6-8个场景,每个有 id/name/description/triggers\n- clues: 10-12条线索,格式为\"线索名(地点):描述\"\nNPC数量: 7-10个"
	case "medium":
		return "- scenes: 4-6个场景,每个有 id/name/description/triggers\n- clues: 7-10条线索,格式为\"线索名(地点):描述\"\nNPC数量: 4-7个"
	default: // short
		return "- scenes: 3-4个场景,每个有 id/name/description/triggers\n- clues: 5-7条线索,格式为\"线索名(地点):描述\"\nNPC数量: 1-4个"
	}
}

func applyGuardrails(draft *ScenarioDraft, req ScenarioCreationRequest) {
	draft.Name = firstNonEmpty(req.Name, draft.Name)
	draft.MinPlayers = req.MinPlayers
	draft.MaxPlayers = req.MaxPlayers
	draft.Difficulty = firstNonEmpty(req.Difficulty, draft.Difficulty)
	if draft.Author == "" {
		draft.Author = "agent-team"
	}
}

func loadRecentNPCNameBlacklist(limit int) []string {
	if limit <= 0 || models.DB == nil {
		return nil
	}
	var scenarios []models.Scenario
	if err := models.DB.Order("created_at DESC").Limit(limit * 3).Find(&scenarios).Error; err != nil {
		log.Printf("[scripter] load recent npc blacklist failed: %v", err)
		return nil
	}
	seen := map[string]bool{}
	names := make([]string, 0, limit)
	for i := range scenarios {
		if err := scenarios[i].DecodeData(); err != nil {
			continue
		}
		for _, npc := range scenarios[i].Content.Data.NPCs {
			name := normalizeNPCName(npc.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
			if len(names) >= limit {
				return names
			}
		}
	}
	return names
}

func loadScenarioTitleSamples(sampleSize int) []string {
	if sampleSize <= 0 || models.DB == nil {
		return nil
	}
	var count int64
	if err := models.DB.Model(&models.Scenario{}).Count(&count).Error; err != nil {
		log.Printf("[scripter] count scenario titles failed: %v", err)
		return nil
	}
	limit := int(count)
	if count > int64(sampleSize) {
		limit = sampleSize
	}
	var scenarios []models.Scenario
	if count <= int64(sampleSize) {
		if err := models.DB.Order("created_at DESC").Find(&scenarios).Error; err != nil {
			log.Printf("[scripter] load all scenario titles failed: %v", err)
			return nil
		}
	} else if err := models.DB.Order("RANDOM()").Limit(limit).Find(&scenarios).Error; err != nil {
		log.Printf("[scripter] sample scenario titles failed: %v", err)
		return nil
	}
	titles := make([]string, 0, len(scenarios))
	seen := map[string]bool{}
	for _, scenario := range scenarios {
		title := normalizeScenarioTitle(scenario.Name)
		if title == "" || seen[title] {
			continue
		}
		seen[title] = true
		titles = append(titles, title)
	}
	return titles
}

func formatNPCNameBlacklist(names []string) string {
	if len(names) == 0 {
		return "(无)"
	}
	return "- " + strings.Join(names, "\n- ")
}

func formatScenarioTitleBlacklist(names []string) string {
	if len(names) == 0 {
		return "(无)"
	}
	return "- " + strings.Join(names, "\n- ")
}

func normalizeScenarioTitle(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, " `\"'，。；;：:（）()【】[]《》")
	return strings.TrimSpace(name)
}

func normalizeNPCName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, " `\"'，。；;：:（）()【】[]")
	return strings.TrimSpace(name)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// grepRulebook searches the rulebook for exact keyword matches and returns
// surrounding context (30 lines before/after each hit), capped at 2000 chars.
func grepRulebook(keyword string) string {
	hits := rulebook.GrepRuleBook(keyword)
	if len(hits) == 0 {
		return ""
	}

	const maxLen = 20

	var sb strings.Builder
	for i, h := range hits {
		s := h.Text
		if len(s) > maxLen {
			runes := []rune(s)
			if len(runes) > maxLen {
				s = string(runes[:maxLen]) + "..."
			}
		}
		sb.WriteString(fmt.Sprintf("[%v] Hit Line: %v Content: %v\n", i+1, h.LineNum, s))
	}
	return strings.TrimSpace(sb.String())
}
