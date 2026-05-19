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
// Prompts for the story-first generation pipeline
// ---------------------------------------------------------------------------

var storySystemPrompt = `<role>COC7地方背景设定编剧</role>
<task>将用户主题/时代/brief/随机约束整理成事件发生地背景；story阶段只生成现实背景土壤,不要生成神话元素或完整剧情。</task>
<config nsfw="true" violent="true" sexual="true"/>
<output>只输出背景设定正文；不要JSON、Markdown、工具调用、标题或解释。</output>
<background_design>
- 必须写清事件发生地点：自然地理、人文地理、特色建筑。
- 必须写清社会背景：权力运作模式、地方精英/机构关系、执法与安全机构。
- 必须写清经济与产业：当地支柱产业、交通是否便利、商店/银行/旅店等设施是否齐全、是否存在走私等非法经济活动、财富分配模式。
- 必须写清民俗文化：信仰、风俗、地方禁忌、节庆或行业规矩。
- 各要素不是孤立清单，必须相互交织、彼此印证：地理影响交通和执法，产业影响权力结构，财富分配影响地方矛盾，民俗回应现实压力。
- 可以留下表面事件、地方传闻、社会压力或调查入口，但不要解释为神话。</background_design>
<ban>
- 禁止输入或点名神话实体、神话生物、旧日支配者、外神、神话法术、神话典籍、眷族、仪式、幕后真相、结局代价。
- 禁止设计BOSS、怪物行动线、神话因果链、胜负条件、完整线索链。
- 禁止伪科学、宏大工程、国家级设施、军事/核能/航天/深海/高能物理/绝密研究。
- 不要只写氛围；要写出地方社会如何运行，以及为什么这里适合发生调查故事。</ban>
`

var outlineSystemPrompt = `<role>COC7模组结构化改编师</role>
<task>基于story做可跑团大纲结构化和规则核验；不得重新创作核心故事,只把已确定的神话威胁、NPC动机、反转和结局代价改编为可执行场景/线索/胜负条件。</task>
<config nsfw="true" violent="true" sexual="true"/>
<rulebook>` + rulebook.RulebookDir + `</rulebook>
<tools>
search: {"action":"search","query":"自然语言规则查询"}
read_rulebook_const: {"action":"read_rulebook_const","constant":"rulebook_dir|rulebook_detail_dir|aliens|books|great_old_ones_and_gods|monsters|mythos_creatures|spells"}
yield: {"action":"yield"}
response: {"action":"response","outline":"大纲纯文本"}
</tools>
<exec>
- 只输出JSON数组。
- 第1轮必须 read_rulebook_const monsters + mythos_creatures 后 yield；第1轮禁 response；禁 [{"action":"yield"}]。
- 查询批次可含多个 search/read_rulebook_const；yield只能作最后一项且前面至少有一个查询。
- 有工具结果后: 信息不足则继续查询+yield；信息足够则 response，禁止空yield。
- 禁直接response；至少完成一次查询批次并读取结果。</exec>
<adapt>
- story是核心设计来源；不得重选核心神话威胁、推翻NPC真实动机、重写核心反转、替换结局代价或改变调查员介入理由。
- 你的职责是规则核验与可跑团结构化: 补齐公开信息、地图、场景、行动路径、NPC行动线、线索冗余、属性范围、可执行胜负条件和代价呈现。
- 保留outline阶段规则书工具调用,用于核验story中的怪物/法术/典籍/神祇,并补齐怪物属性范围、规则书称谓和可用限制。
- 只有当story中的神话元素无法在规则书中成立时,才允许最小替换；替换必须保持story中的因果功能、威胁位置、NPC动机和结局代价,并在大纲中说明替换理由。</adapt>
<outline_req>
- 含:背景、story指定叙事结构的跑团落地方式、主要NPC(沿用姓名+动机+属性范围)、场景列表、线索链、胜/败/部分胜利。
- BOSS和神话元素必须来自COC规则书或已被最小规则替换；NPC数值:人类15-90,怪物按规则书。
- 线索冗余:至少2条路径通向关键信息；至少1条非运气推理胜利路径。
- 长期奖励(典籍/道具/法术/盟友/资源)只可来自story已确定的来源或其直接后果；必须写清路径、来源、代价、风险、后果；禁无条件白送。
- 输出应足够draft阶段直接转成JSON,但不要输出完整模组JSON。</outline_req>
<variety>
- 在不改写story核心事实的前提下,细化时代细节/地点/调查玩法/胜利代价/奖励呈现。
- 禁默认套路堆叠:孤岛/灯塔/渔村/旧宅/失踪教授/邪教仪式/梦境真相/古书召唤/地下室Boss；若story中使用,只能通过场景关系和玩法重塑,不得替换核心事实。
- 允许高风险高收益结局。</variety>
<monster_entry require="pick>=1">
身份伪装;间接感知/认知失调;环境异常本身;叙事反转。禁“黑暗中突然出现/踹门冲入”。</monster_entry>
<npc_req>
- 沿用story中主要NPC姓名和真实动机；只可补齐态度、行动线、属性范围和跑团可用信息。
- 至少1人真实立场反外表/身份；至少1人有独立行动线；至少1人无辜且拒信超自然。
- 禁主要NPC全是知情者。
- NPC name 必须具体可称呼；禁职业/身份泛称；禁复用近期NPC黑名单。</npc_req>
<clues>[真实]=核心真相且冗余；[隐藏]=需特定技能成功的深层线索。</clues>
<puzzle>解谜必须实际帮助困境；至少一条非运气推理胜利路径。</puzzle>
<ban>
- 无叙事功能gore；逻辑跳跃；因果断链；计划依赖调查员恰好按顺序行动。
- 伪科学/科学术语解释神话:高维度/拓扑/物理常数/量子等。
- 工程机关/科技设备解释神话:机械装置、电子设备、声波/振动/频率、传感器、药剂、催眠器、信号标记、安全区/诱导区等。
- 抽象情感/象征祭品机制:异化的人类情感、人性温度、珍贵之物、真正的爱/记忆/牺牲/信念等作为锚点/钥匙/封印/信标/唯一解法。
- 空泛神秘词:某个古老存在/无法描述/超越理解/物理法则等。
- 奇幻、玄幻小说化。</ban>
`

// draftPrompt has 3 format args: outline, scenarioExample, lengthSpec
const draftPrompt = `<task>将模组大纲转换为完整JSON模组；严格遵循示例结构。</task>
<config nsfw="true" violent="true" sexual="true"/>
<outline>
%s
</outline>
<json_example>
%s
</json_example>
<out>仅输出JSON,无其他文字。</out>
<fields>
- system_prompt: KP指导2-3句。
- setting: 开局公开时代/地点/日常背景/气氛/社会常识；禁幕后真相、怪物/神话实体、仪式、隐藏身份、反转、胜负条件、后续剧情；自然像KP桌边介绍,非设定集/梗概。
- intro: 玩家可听的开场；第二人称；眼前处境、表面任务、委托公开信息、可立即行动目标；禁暗示真凶/超自然来源/隐藏线索/结局/“背后隐藏着”；具体简洁有现场感。
%s
- game_start_slot: 0-47,每槽30分钟；按剧情选。
- map_description: 简洁文字地图；主要地点、空间关系、移动路径；辅助KP定位。
- npcs: name/description/attitude/stats；name具体可称呼,禁近期黑名单/职业身份泛称。
- clues: 前缀格式 "[真实]名(地点):描述" / "[误导]名(地点):描述" / "[隐藏]名(地点):描述(需XXX检定)"。
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
结构20: 场景/NPC/线索/胜负齐全,lose/partial有意义。
线索15: 含[真实]/[隐藏],有冗余路径。
规则15: 神话元素来自规则书,NPC属性合规。
可玩15: 有真实决策,胜负依赖玩家行为非固定剧情。
文本5: setting/intro自然可读、无剧透、仅开局公开信息。
新颖15: 有意外设计；怪物登场用伪装/错位/环境/反转之一；避套路。
悬疑15: 未知与转折；恐惧来自认知失调/未知,非gore/伪科学。
加权: 保留brief核心因果/NPC动机/介入理由/升级/反转；奖励有条件/来源/风险/后果,不得因永久奖励扣分。</score>
<must_fix>
- 缺 lose_condition 或 partial_wins；缺[隐藏]线索；NPC全知情者；NPC名复用黑名单或职业泛称。
- 三幕剧套壳无转折；怪物/神话不符规则书；怪物直白冲出/黑暗出现且无新颖手法。
- 无叙事功能gore；因果断链；NPC无动机；背离brief核心故事。
- 伪科学/科学术语解释神话；用机械装置/电子设备/声波/振动/频率/传感器/药剂/催眠器/信号标记/安全区解释神话现象；用抽象情感/象征祭品(人性温度/珍贵之物/爱/记忆/牺牲/信念)作锚点/钥匙/封印/唯一解法；空泛神秘描述。
- setting/intro 剧透幕后真相、怪物/实体、仪式、隐藏身份、反转、胜负或后续；或像设定集/梗概/模板。</must_fix>
<pass>score>=80 且 must_fix为空。</pass>`

const revisionPrompt = `<task>根据QA反馈修订模组JSON。</task>
<out>仅输出修订后的完整JSON,无其他文字。</out>
<config nsfw="true" violent="true" sexual="true"/>
<outline>
%s
</outline>
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

var randomTopicSystemPrompt = `<role>COC地方背景设定生成器</role>
<task>为自动brief生成一个可支撑调查故事的事件发生地背景设定。</task>
<need>背景要贴近日常、小尺度、普通人可接触；适合调查入口、地方传闻、人物关系和后续神话介入。</need>
<fields>
- 事件发生地点：写清自然地理、人文地理、特色建筑。
- 社会背景：写清权力运作模式、执法与安全机构。
- 经济与产业：写清当地支柱产业、交通便利程度、商店/银行/旅店等设施是否齐全、是否存在走私等非法经济活动、财富分配模式。
- 民俗文化：写清信仰、风俗、地方禁忌或节庆。
- 交织关系：说明地理、社会、经济、民俗如何互相印证，并形成可引出COC调查的压力或矛盾。</fields>
<rules>
- 各要素不能孤立罗列，必须彼此因果关联：例如偏僻地理影响交通与执法，单一产业影响权力结构，产业衰败影响民俗信仰或秘密组织。
- 保持现实生活质感，不要直接写幕后神话真相、怪物、仪式或结局。
- 可以留下适合story阶段发展的异常压力，但不要替story阶段指定唯一谜底。</rules>
<ban>宏大工程、国家级设施、军事/核能/航天/深海/高能物理/绝密研究；如核电站、反应堆、导弹基地、航天基地、粒子加速器、深海基地。禁止输出随机元素清单。</ban>
<out>仅输出一段300-600字的简中背景设定正文；不要JSON、Markdown、编号、标题或解释。</out>`

var storyElementSystemPrompt = `<role>COC地方特色列举器</role>
<task>根据已有背景列举可继续加入的地方特色。</task>
<need>地方特色必须贴合当前事件发生地，能增强地方感、日常生活质感和可调查性。</need>
<prefer>地方饮食、口音称谓、行业规矩、集市日、地方节庆、民间禁忌、特色建筑、手工业、运输习惯、旅店/酒馆习俗、商会规矩、治安潜规则、地方报纸、学校/教会/会社活动、财富炫耀方式、穷人互助方式。</prefer>
<ban>不要列神话实体、怪物、法术、仪式、幕后真相、结局、抽象主题词、宏大设施或通用背景词。</ban>
<out>仅输出地方特色名称列表；每行一个；正好200个；无编号、解释、标题或描述句。</out>`

var geographyElementSystemPrompt = `<role>COC事件发生地候选列举器</role>
<task>根据用户给定阶段列举20个可用于事件发生地的候选。</task>
<rules>
- 严格按用户要求的阶段输出候选，不得偷换成行政区划清单。
- country阶段输出具体国家或具体政权范围。
- 非country阶段只输出类型/形态/区位模式，不输出具体地名、真实行政区名、真实城市名或真实街区名。
- natural_geography阶段必须输出自然地理/地形/水文/气候约束类型。
- human_geography阶段必须输出人文地理/聚落形态/人口构成/地方空间组织类型。
- economy_transport阶段必须输出交通可达性、产业空间或非法经济空间类型。
- landmark_stage阶段必须输出特色建筑、地标或关键公共空间类型。
- 只输出现实地理/人文地理候选，不输出神话元素、怪物、仪式、幕后真相。
- 候选应适合COC调查故事，具有地方社会、交通、产业、执法或民俗延展空间。
- 每行一个名称，正好20个，不要编号、解释、标题或描述句。</rules>`

// ---------------------------------------------------------------------------
// Tool-call types for outline & QA phases
// ---------------------------------------------------------------------------

type pipelineToolCall struct {
	Action   string         `json:"action"`
	Keyword  string         `json:"keyword,omitempty"`  // grep (kept for backward compat)
	Query    string         `json:"query,omitempty"`    // search
	Constant string         `json:"constant,omitempty"` // read_rulebook_const
	Brief    string         `json:"brief,omitempty"`    // response (story phase)
	Outline  string         `json:"outline,omitempty"`  // response (outline phase)
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
	"大世界探索, 在开放世界中自由探索, 线索分布在各个角落",
	"密室逃脱，限时破解封闭空间内的连环谜题",
	"间谍潜入，伪装身份渗透目标组织，窃取情报并安全撤离",
	"都市怪谈，调查现代都市中的异常现象，揭开传闻背后的真相",
	"物品收集，寻找并组合散落世界各地的神话物品，解锁隐藏剧情和能力",
	"社会潜入，利用身份、关系和话术进入封闭社群或组织核心",
	"追踪狩猎，沿着踪迹、目击记录和异常痕迹寻找移动中的神话目标",
	"航程/旅途结构，交通工具或迁徙路线上的每一站都揭开一层真相",
	"拍卖/交易会结构，多方势力争夺同一神话物品，玩家可谈判、盗取或毁弃",
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
	// Defaults
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

	reqJSON, _ := json.Marshal(req)
	log.Printf("[scripter] 开始故事优先生成 req=%s", reqJSON)

	// Load agents: architect + qa_guard + parser (JSON fixer)
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
	writer, err := loadSingleAgent(models.AgentRoleWriter)
	if err != nil {
		return ScenarioCreationOutput{}, err
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
	debugf("script", "theme: %v", req.Theme)

	npcNameBlacklist := loadRecentNPCNameBlacklist(200)
	debugf("script", "npc blacklist count: %d", len(npcNameBlacklist))

	geographyChain, geoErr := generateGeographyChain(ctx, writer, req.Era)
	if geoErr != nil {
		log.Printf("[scripter] geography chain generation failed: %v", geoErr)
	}
	storyBrief, err := generateStoryBrief(ctx, architect, req.Era, req.Theme, geographyChain)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("story brief 生成失败: %w", err)
	}
	storyBackgroundAddendum := ""
	if strings.TrimSpace(storyBrief) != "" {
		addendum, addErr := generateRandomStoryBackgroundAddendum(ctx, architect, storyBrief)
		if addErr != nil {
			log.Printf("[scripter] story random background addendum failed: %v", addErr)
		} else if strings.TrimSpace(addendum) != "" {
			storyBackgroundAddendum = addendum
		}
		polishedBrief, polishErr := polishStoryBrief(ctx, writer, storyBrief)
		if polishErr != nil {
			log.Printf("[scripter] story brief polish failed: %v", polishErr)
		} else if strings.TrimSpace(polishedBrief) != "" {
			storyBrief = polishedBrief
		}
		req.Brief = storyBrief
		log.Printf("[scripter] story len=%d", len([]rune(req.Brief)))
		debugf("script", "story brief: %v", req.Brief)
		debugf("script", "story background addendum: %v", storyBackgroundAddendum)
	}

	// Outline: structure the story into a playable module outline.
	outline, err := generateOutline(ctx, architect, req, storyBrief, storyBackgroundAddendum, npcNameBlacklist)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("outline 生成失败: %w", err)
	}
	log.Printf("[scripter] outline len=%d", len([]rune(outline)))

	// Phase 2: Draft (pure JSON generation; parser as JSON fixer)
	draft, err := buildDraft(ctx, architect, parser, outline, req.TargetLength, npcNameBlacklist)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("phase2 draft 失败: %w", err)
	}
	applyGuardrails(&draft, req)
	log.Printf("[scripter] phase2 draft name=%q scenes=%d npcs=%d clues=%d",
		draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))

	// Phase 3: QA + Iteration (up to 2 revisions, with grep tool calls)
	var qaResult qaGuardResult
	for i := 0; i < 30; i++ {
		if ctx.Err() != nil {
			return ScenarioCreationOutput{}, ctx.Err()
		}
		qaResult, err = runQA(ctx, qaAgent, parser, req, draft, npcNameBlacklist)
		if err != nil {
			log.Printf("[scripter] phase3 QA失败 iter=%d: %v", i, err)
			return ScenarioCreationOutput{}, fmt.Errorf("phase3 QA 失败: %w", err)
		}
		log.Printf("[scripter] phase3 QA iter=%d score=%d pass=%v must_fix=%d",
			i, qaResult.Score, qaResult.Pass, len(qaResult.MustFix))

		if qaResult.Pass {
			return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: i + 1}, nil
		}

		// Last iteration — don't revise, just return best effort
		if i == 2 {
			break
		}

		// Revise draft based on QA feedback
		revised, revErr := reviseDraft(ctx, architect, parser, draft, qaResult.MustFix, outline, npcNameBlacklist)
		if revErr != nil {
			log.Printf("[scripter] revision 失败 iter=%d: %v", i, revErr)
			break // return best effort
		}
		applyGuardrails(&revised, req)
		draft = revised
		log.Printf("[scripter] revision iter=%d done", i)
	}

	// Return best effort even if QA didn't pass
	return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: 3}, nil
}

// ---------------------------------------------------------------------------
// Phase 1: Generate story brief, then outline
// ---------------------------------------------------------------------------

func generateStoryBrief(ctx context.Context, writer agentHandle, era string, threat string, geographyChain []string) (string, error) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: writer.systemPrompt(storySystemPrompt)},
		{Role: "user", Content: fmt.Sprintf("请根据时代、威胁参考和事件发生地约束生成背景设定。只生成现实背景，不要点名神话实体/怪物/法术/典籍，不要设计完整剧情。威胁参考只能转化为社会压力、地方矛盾、传闻或调查入口。事件发生地约束中只有国家是具体地点，后续分别是自然地理、人文地理、经济交通、特色建筑/地标类型；你需要基于这些类型自行创造合理的虚构区域、聚落、街区或建筑名称，并让它们符合该国家与时代。背景必须把自然地理、人文地理、特色建筑、权力运作、执法安全、经济产业、交通便利性、商店/银行/旅店设施、非法经济、财富分配、民俗文化相互交织起来。\n\n时代：%s\n威胁参考：%s\n事件发生地约束：%s", era, threat, strings.Join(geographyChain, " → "))},
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	log.Printf("[story] single pass")
	raw, err := writer.provider.Chat(ctx, msgs)
	if err != nil {
		return "", err
	}
	brief := strings.TrimSpace(llm.StripCodeFence(raw))
	debugf("story", "raw: %v", raw)
	if brief == "" {
		return "", fmt.Errorf("story 返回空内容")
	}
	return brief, nil
}

func generateGeographyChain(ctx context.Context, architect agentHandle, era string) ([]string, error) {
	stages := []struct {
		Key      string
		Mode     string
		Examples string
	}{
		{Key: "country", Mode: "具体国家或具体政权范围", Examples: "美国、英国、法兰西第三共和国、埃及王国、英属印度"},
		{Key: "natural_geography", Mode: "自然地理/地形/水文/气候约束类型，不输出具体地名", Examples: "潮湿三角洲、内陆盐碱盆地、林木覆盖的山谷、风暴频发海岸、喀斯特丘陵、冻土边缘河谷、雾重的湖沼地带"},
		{Key: "human_geography", Mode: "人文地理/聚落形态/人口构成/地方空间组织类型，不输出具体地名", Examples: "移民混居矿镇、教会控制的山坡聚落、铁路公司镇、季节性猎户营地、族裔分隔港区、庄园佃农村落、疗养院附属小镇、城市、大都会"},
		{Key: "economy_transport", Mode: "交通可达性、支柱产业、商业设施或非法经济空间类型，不输出具体地名", Examples: "单线铁路末端、河运转运码头、衰败煤矿产业带、走私盐酒通道、银行兼邮局的商业街、每周集市牲畜交易场、伐木铁路支线"},
		{Key: "landmark_stage", Mode: "特色建筑、地标或关键公共空间类型，不输出具体地名", Examples: "旧海关楼、废弃采石场、木栈码头、市场钟楼、铁路水塔、山口桥梁、砖砌警署兼拘留室、地方报社小楼"},
	}
	chain := make([]string, 0, len(stages))
	msgs := []llm.ChatMessage{{Role: "system", Content: architect.systemPrompt(geographyElementSystemPrompt)}}
	for _, stage := range stages {
		items, err := generateGeographyCandidates(ctx, architect, &msgs, era, stage.Key, stage.Mode, stage.Examples, chain)
		if err != nil {
			return chain, err
		}
		if len(items) == 0 {
			log.Printf("[scripter] geography stage=%q candidates=0", stage.Key)
			return chain, fmt.Errorf("%s 候选为空", stage.Key)
		}
		choice := items[rand.Intn(len(items))]
		chain = append(chain, choice)
		log.Printf("[scripter] geography stage=%q candidates=%v chosen=%q", stage.Key, items, choice)
	}
	return chain, nil
}

func generateGeographyCandidates(ctx context.Context, architect agentHandle, msgs *[]llm.ChatMessage, era string, stageKey string, mode string, examples string, chain []string) ([]string, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	selected := "无，第一轮先选择具体国家或政权范围"
	if len(chain) > 0 {
		selected = strings.Join(chain, " → ")
	}
	prompt := fmt.Sprintf("已随机选中的前置约束：%s\n现在进入下一阶段：%s\n本阶段任务：根据前置约束继续列举候选，不要重做上一阶段。\n时代：%s\n输出要求：%s\n示例范围：%s\n\n请只输出本阶段的20个候选。", selected, stageKey, era, mode, examples)
	*msgs = append(*msgs, llm.ChatMessage{Role: "user", Content: prompt})
	raw, err := architect.provider.Chat(ctx, *msgs)
	if err != nil {
		return nil, err
	}
	*msgs = append(*msgs, llm.ChatMessage{Role: "assistant", Content: raw})
	items := parseElementNames(raw)
	if len(items) == 0 {
		return nil, fmt.Errorf("地理候选列表为空")
	}
	return items, nil
}

func generateRandomStoryBackgroundAddendum(ctx context.Context, architect agentHandle, story string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	items, err := generateStoryElementCandidates(ctx, architect, story)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", fmt.Errorf("未生成可用地方特色")
	}
	randomElem := items[rand.Intn(len(items))]
	log.Printf("[scripter] local feature candidates=%d chosen=%q", len(items), randomElem)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: architect.systemPrompt(`<role>COC地方特色补充编辑</role>
<task>基于已有背景和一个随机地方特色，生成后置背景补充；不要改写背景本身。</task>
<rules>
- 不得复述、改写或扩写背景主干。
- 只围绕地方特色补充日常生活、经济产业、民俗文化、治安或人际规矩中的设定。
- 随机地方特色必须影响至少两个背景维度，不能只提到一次。
- 补充设定要与已有背景互相印证，不能孤立堆砌。
- 不得新增神话来源、幕后真相、NPC动机、线索路径或结局代价。
- 不得加入机械/科技/声波/药剂解释神话，不得加入抽象情感祭品或象征钥匙。
- 只输出地方特色背景补充正文，不要JSON、标题或修改说明。</rules>`)},
		{Role: "user", Content: fmt.Sprintf("<background>\n%s\n</background>\n\n<random_local_feature>\n%s\n</random_local_feature>", story, randomElem)},
	}
	raw, err := architect.provider.Chat(ctx, msgs)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(llm.StripCodeFence(raw)), nil
}

func generateStoryElementCandidates(ctx context.Context, architect agentHandle, story string) ([]string, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: architect.systemPrompt(storyElementSystemPrompt)},
		{Role: "user", Content: "请根据以下背景列举200个可加入的地方特色。\n\n" + story},
	}
	raw, err := architect.provider.Chat(ctx, msgs)
	if err != nil {
		return nil, err
	}
	items := parseElementNames(raw)
	if len(items) == 0 {
		return nil, fmt.Errorf("地方特色列表为空")
	}
	return items, nil
}

func parseElementNames(raw string) []string {
	raw = llm.StripCodeFence(strings.TrimSpace(raw))
	raw = strings.ReplaceAll(raw, "，", "\n")
	raw = strings.ReplaceAll(raw, ",", "\n")
	raw = strings.ReplaceAll(raw, "、", "\n")
	lines := strings.Split(raw, "\n")
	items := make([]string, 0, len(lines))
	seen := map[string]bool{}
	for _, line := range lines {
		name := normalizeElementName(line)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, name)
	}
	return items
}

func normalizeElementName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "-•*· ")
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, ".、)"); idx >= 0 && idx <= 4 {
		prefix := strings.TrimSpace(s[:idx])
		if prefix != "" {
			allDigits := true
			for _, r := range prefix {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				s = strings.TrimSpace(s[idx+1:])
			}
		}
	}
	s = strings.Trim(s, " `\"'，。；;：:（）()【】[]《》")
	if s == "" || strings.Contains(s, "：") || strings.Contains(s, ":") {
		return ""
	}
	if len([]rune(s)) > 40 {
		return ""
	}
	return strings.TrimSpace(s)
}

func polishStoryBrief(ctx context.Context, writer agentHandle, brief string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: writer.systemPrompt(`<role>COC背景设定润色编辑</role>
<task>润色事件发生地背景设定文本,提升因果清晰度、可读性和地方质感。</task>
<config nsfw="true" violent="true" sexual="true"/>
<rules>
- 不得新增、删除或改写核心背景事实。
- 不得加入神话实体、神话生物、旧日支配者、外神、神话法术、神话典籍、眷族、仪式、幕后真相、结局代价。
- 不得加入机械/科技/声波/药剂解释异常,不得加入抽象情感祭品或象征钥匙。
- 保持简中,800-1600字左右；只输出润色后的背景设定正文,不要JSON/标题解释/修改说明。</rules>`)},
		{Role: "user", Content: "请润色以下背景设定,保持事实不变:\n\n" + brief},
	}
	raw, err := writer.provider.Chat(ctx, msgs)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(llm.StripCodeFence(raw)), nil
}

func generateOutline(ctx context.Context, architect agentHandle, req ScenarioCreationRequest, storyBrief string, storyBackgroundAddendum string, npcNameBlacklist []string) (string, error) {
	reqJSON, _ := json.Marshal(req)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: architect.systemPrompt(outlineSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf("请至少查看一次怪物和神话生物列表来核验story中的神话元素,再把story结构化为可跑团模组大纲。不得重选核心神话威胁、推翻NPC动机、重写核心反转或结局代价；只有规则书不成立时才做最小替换。若存在background_addendum，只能把它作为地点/社会/经济/民俗的后置背景补充吸收进大纲，不得反向改写story核心事实。\n\n<request_json>\n%s\n</request_json>\n\n<story>\n%s\n</story>\n\n<background_addendum>\n%s\n</background_addendum>\n\n<recent_npc_name_blacklist>\n%s\n</recent_npc_name_blacklist>", string(reqJSON), storyBrief, storyBackgroundAddendum, formatNPCNameBlacklist(npcNameBlacklist))},
	}

	const maxIter = 30
	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		log.Printf("[outline] iter=%d", iter+1)

		var raw string
		var err error
		for i := 0; i < 3; i++ { // retry loop for transient LLM errors
			raw, err = architect.provider.Chat(ctx, msgs)
			if err != nil {
				return "", err
			}
			if raw != "" {
				break
			}
		}
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		debugf("outline", "raw: %v", raw)

		calls := parsePipelineCalls(ctx, raw)
		if len(calls) == 0 {
			// If no tool calls parsed, treat raw text as outline directly
			log.Printf("[outline] iter=%d 无tool call,使用原始文本作为大纲", iter+1)
			return strings.TrimSpace(raw), nil
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
			if c.Action == "response" && c.Outline != "" {
				log.Printf("[outline] iter=%d response 完成", iter+1)
				return strings.TrimSpace(c.Outline), nil
			}
		}

		// Execute search calls
		feedback := executeSearchCalls(ctx, calls, "outline")
		if feedback == "" {
			return "", fmt.Errorf("outline 未返回有效 tool call")
		}
		msgs = append(msgs, llm.ChatMessage{
			Role:    "user",
			Content: feedback,
		})
	}

	return "", fmt.Errorf("outline 达到最大迭代仍未返回 response")
}

// ---------------------------------------------------------------------------
// Phase 2: Build Draft (pure JSON, no tool calls)
// ---------------------------------------------------------------------------

func buildDraft(ctx context.Context, architect, fixer agentHandle, outline string, targetLength string, npcNameBlacklist []string) (ScenarioDraft, error) {
	userMsg := fmt.Sprintf(draftPrompt, outline, scenarioExample, lengthSpec(targetLength))
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

func runQA(ctx context.Context, qaAgent agentHandle, parser agentHandle, req ScenarioCreationRequest, draft ScenarioDraft, npcNameBlacklist []string) (qaGuardResult, error) {
	reqJSON, _ := json.Marshal(req)
	draftJSON, _ := json.Marshal(draft)

	userMsg := fmt.Sprintf("审查以下 COC 模组的质量, 是否符合逻辑, 剧情是否胡乱编造。\n\n【原始需求】\n%s\n\n【近期已用 NPC 名字黑名单，npcs[].name 禁止复用】\n%s\n\n【模组草案】\n%s",
		string(reqJSON), formatNPCNameBlacklist(npcNameBlacklist), string(draftJSON))

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

func reviseDraft(ctx context.Context, architect, fixer agentHandle, draft ScenarioDraft, mustFix []string, outline string, npcNameBlacklist []string) (ScenarioDraft, error) {
	draftJSON, _ := json.Marshal(draft)
	issues := strings.Join(mustFix, "\n- ")

	userMsg := fmt.Sprintf(revisionPrompt, outline, string(draftJSON), issues, scenarioExample)
	userMsg += "\n\n【近期已用 NPC 名字黑名单，修订后的 npcs[].name 禁止复用】\n" + formatNPCNameBlacklist(npcNameBlacklist)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是 COC TRPG 模组修订器。根据QA反馈修订模组。仅输出修订后的完整 JSON,不要有其他文字。"},
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
	err := json.Unmarshal([]byte(raw), &calls)
	if err == nil {
		return calls
	}
	const maxIter = 10
	for i := 0; i < maxIter; i++ {
		raw, err = RepairJSON(c, raw, err, pipelineToolCallExample)
		if err == nil {
			err = json.Unmarshal([]byte(raw), &calls)
			if err == nil {
				break
			}
		}
	}
	return calls
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
