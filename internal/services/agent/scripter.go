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
// Prompts for the Seed → Bible → Verify → Draft → QA pipeline
// ---------------------------------------------------------------------------

var seedSystemPrompt = `<role>COC7单页敏捷种子设计师</role>
<task>把用户brief、时代、主题约束、地理链和近期黑名单压缩成可扩展的ScenarioSeed。目标是短、快、强的单页骨架，不写完整剧本。</task>
<config nsfw="true" violent="true" sexual="true"/>
<output>只输出合法JSON对象，字段必须匹配ScenarioSeed；不要Markdown、标题、解释或代码围栏。</output>
<priority>
- 如果用户brief非空，必须作为最高优先级创意输入吸收并保留核心意图，不得被随机主题或地理背景覆盖。
- 随机主题和地理链只用于补强舞台、玩法与新颖约束, 不要完全依赖它们。
</priority>
<schema>
{
  "core_stage": "一句强画面冲突舞台",
  "local_soil": "现实地方土壤：地理/社会/经济/民俗如何交织",
  "investigator_hook": "调查员介入点和开局表面任务",
  "no_investigator_outcome": "调查员不来时会如何恶化",
  "threat_direction": ["威胁来源候选或方向，不必完成规则核验"],
  "core_twist": "核心反转或误读",
  "gameplay_tags": ["玩法标签"],
  "sandbox": {"core": ["必须发生或必须可抵达的核心场景"], "optional": ["可选探索地带"], "background": ["背景幕或传闻层"]},
  "key_clue_redundancy": ["关键情报及至少两条潜在线索路径"],
  "props_and_npc_seeds": ["可跑团道具/NPC种子"],
  "novelty_limits": ["必须避免的俗套或必须采用的新颖手法"],
  "brief_preserved": "用户brief的核心意图如何被保留"
}
</schema>
<rules>
- 可以提出神话威胁方向，但不要展开成完整规则裁定。
- 必须有核心/可选/背景三层沙盒和线索冗余雏形。
- 禁默认套路堆叠：孤岛、灯塔、渔村、旧宅、失踪教授、邪教仪式、梦境真相、古书召唤、地下室Boss。若输入强制使用，必须写出重塑方式。
- 禁伪科学、宏大工程、国家级设施、军事/核能/航天/深海/高能物理/绝密研究。
</rules>
`

var bibleSystemPrompt = `<role>COC7结构化剧情圣经设计师</role>
<task>把ScenarioSeed扩展为完整但可控的ScenarioBible，用反派计划反推、幕结构、NPC动机、场景功能、蛛网线索和胜败代价支撑后续director-ready JSON。</task>
<config nsfw="true" violent="true" sexual="true"/>
<output>只输出合法JSON对象，字段必须匹配ScenarioBible；不要Markdown、标题、解释或代码围栏。</output>
<schema>
{
  "title_working": "暂定标题",
  "premise": "一句话前提",
  "public_setup": "开局公开信息，不剧透",
  "behind_truth": "幕后真相，可含待核验神话威胁",
  "antagonist_plan": {"actor": "反派/怪物/组织", "goal": "目标", "method": "方法", "if_unopposed": "无人阻止的结果"},
  "mythos_elements": ["需要规则核验的怪物/神祇/典籍/法术/物品/规则名"],
  "timeline": ["已经发生/将发生的关键节点，含倒计时或升级"],
  "acts": [{"name": "幕名", "purpose": "调查功能", "turning_point": "转折"}],
  "scenes": {"core": [{"id":"稳定英文或拼音id","name":"场景名","function":"场景功能","interactive_objects":["可互动对象"],"clues":["线索名"],"checks":["检定/代价"],"danger":"危险","exits":["可推进到的场景id"]}], "optional": [], "background": []},
  "npcs": [{"name":"具体姓名","public_identity":"公开身份","appearance":"可扮演特征","attitude":"初始态度","real_motive":"真实动机","secret":"秘密","action_line":"独立行动线","stats_note":"人类15-90或怪物按规则书"}],
  "clue_web": [{"truth":"关键情报","paths":["路径A：地点+获取方式","路径B：地点+获取方式"],"failure_fallback":"失败后的备用推进"}],
  "win_loss": {"win":"可核查胜利条件","lose":"可核查失败条件","partial":["部分胜利条件"]},
  "long_term_rewards": [{"reward":"长期奖励","source":"来源","path":"取得路径","cost":"代价/风险","consequence":"后果"}],
  "novelty_controls": ["新颖性限制与反套路执行方式"],
  "rule_verification_targets": ["待检索裁定的问题"]
}
</schema>
<rules>
- 允许设计神话威胁，但必须列入 mythos_elements/rule_verification_targets，供后续核验。
- 核心事实必须承接seed：介入理由、地方土壤、威胁功能、核心反转、无人介入后果不能无故改写。
- 每个关键情报至少两条获取路径；至少一条非运气推理胜利路径。
- NPC必须可扮演：具体姓名、公开表象、态度、真实目标/秘密、独立行动线；禁主要NPC全知情者。
- 长期奖励必须来自故事直接后果，写清路径、来源、代价、风险、后果，禁无条件白送。
- 禁伪科学/工程机关/抽象情感祭品解释神话；禁空泛神秘词和奇幻小说化。
</rules>
`

var ruleVerifySystemPrompt = `<role>COC7规则核验架构师</role>
<task>为ScenarioBible中的神话元素、怪物、典籍、法术、神祇和关键规则点生成规则书检索调用；收到检索结果后只做最小规则校正，输出VerifiedBible。</task>
<config nsfw="true" violent="true" sexual="true"/>
<rulebook>` + rulebook.RulebookDir + `</rulebook>
<tools>
search: {"action":"search","query":"自然语言规则查询"}
read_rulebook_const: {"action":"read_rulebook_const","constant":"rulebook_dir|rulebook_detail_dir|aliens|books|great_old_ones_and_gods|monsters|mythos_creatures|spells"}
yield: {"action":"yield"}
response: {"action":"response","verified_bible":{...}}
</tools>
<exec>
- 只输出JSON数组。
- 第1轮必须至少包含 read_rulebook_const monsters、read_rulebook_const mythos_creatures，并按bible内容加入 search；第1轮禁止response。
- 查询批次可含多个 search/read_rulebook_const；yield只能作最后一项且前面至少有一个查询。
- 有工具结果后：信息不足则继续查询+yield；信息足够则输出单个response。
- 禁空数组、空yield、自然语言、Markdown。</exec>
<verify_policy>
- 保持ScenarioBible核心事实不变：因果功能、NPC动机、介入理由、核心反转、结局代价不得重写。
- 只有神话元素无法成立或名称/属性/限制与规则书冲突时才最小替换或添加规则注记。
- 最小替换必须保持原元素在故事中的功能位置，并在rules_notes说明原因。
- 输出的verified_bible必须包含原bible全部主要内容，并增加rules_notes与verified_mythos_elements。
</verify_policy>
<response_schema>
{"verified_bible":{"bible":{...ScenarioBible...},"rules_notes":["规则来源/裁定/最小替换说明"],"verified_mythos_elements":["已核验元素及用途"],"unsupported_replacements":["若有，说明替换前后和原因"]}}
</response_schema>
`

// draftPrompt has 5 format args: verifiedBible, requestJSON, scenarioExample, lengthSpec, npcBlacklist
const draftPrompt = `<task>将VerifiedBible编译为director可直接运行的完整ScenarioDraft JSON；严格对齐models.ScenarioContent和director.go读取方式。</task>
<config nsfw="true" violent="true" sexual="true"/>
<verified_bible>
%s
</verified_bible>
<request_json>
%s
</request_json>
<json_example>
%s
</json_example>
<out>仅输出合法JSON对象，无其他文字。</out>
<length>
%s
</length>
<director_contract>
- content.setting 会被注入KP上下文作为BG：只写开局公开时代/地点/日常背景/表面事件/社会常识；禁止幕后真相、怪物/神话实体、仪式、隐藏身份、反转、胜败条件和后续剧情。
- content.intro 是玩家听到的开场：第二人称，明确玩家起点、表面任务、可立即行动目标；禁止“背后隐藏着”等剧透式表达。
- content.map_description 必须是可导航文字地图：起点、核心地点、可选地点、路径关系、阻碍/入口、可回退路线。
- content.scenes[].description 必须包含：地点/事件、可互动对象、可自动获得线索、需检定线索、危险/代价、可推进到哪些场景。
- content.scenes[].triggers 写可触发条件或进入条件；不要空泛。
- content.npcs[] 必须是稳定角色卡：具体姓名、公开身份、态度、真实目标/秘密写入description或attitude；人类属性15-90，怪物按规则注记；禁止职业/身份泛称和黑名单姓名。
- content.clues[] 必须保持前缀：[真实] / [隐藏] / [误导]；格式包含线索名、地点、获取方式、用途，便于found_clue精确引用。
- content.win_condition / lose_condition / partial_wins 必须是KP可逐条核查的条件，不写抽象文学结局。
</director_contract>
<fields>
- name/description/author/tags/min_players/max_players/difficulty/content 必须完整。
- system_prompt: KP指导2-3句，提醒按线索、NPC动机和胜败条件主持。
- game_start_slot: 0-47，每槽30分钟，按开局选择。
- 长期奖励必须写入scenes/clues/win_condition/partial_wins的可执行路径，含地点、条件、风险、代价、后果。
</fields>
<recent_npc_name_blacklist>
%s
</recent_npc_name_blacklist>
<ban>科学术语解释神话；机械装置/电子设备/声波/振动/频率/传感器/药剂/催眠器/信号标记/安全区解释神话；抽象情感/象征祭品作锚点/钥匙/封印/唯一解法；空泛神秘词；主要NPC全知情。</ban>
`

var qaSystemPrompt = `<role>COC7 director可用性QA</role>
<task>只审查ScenarioDraft是否可被director直接运行、是否规则合规、线索可跑、开局无剧透；不重写创意。</task>
<config nsfw="true" violent="true" sexual="true"/>
<rulebook>` + rulebook.RulebookDir + `</rulebook>
<tools>
search:{"action":"search","query":"自然语言规则查询"}
read_rulebook_const:{"action":"read_rulebook_const","constant":"rulebook_dir|rulebook_detail_dir|aliens|books|great_old_ones_and_gods|monsters|mythos_creatures|spells"}
response:{"action":"response","result":{"score":N,"pass":bool,"strengths":[...],"issues":[...],"must_fix":[...]}}
</tools>
<exec>
- 只允许输出单个JSON数组，数组元素只能是search/read_rulebook_const/response。
- 第1轮必须输出至少1个read_rulebook_const和至少1个search，用于核实草案中的怪物/法术/典籍/神话来源；第1轮禁止response。
- 收到规则书搜索结果后，若信息足够，必须只输出1个response action；response不得和查询混用。
- 若信息仍不足，继续输出至少1个有效查询；禁止空数组、yield、自然语言、Markdown。
</exec>
<checklist total="100">
字段完整10: ScenarioDraft顶层字段和ScenarioContent字段齐全，scenes/npcs/clues数量符合target_length。
Director可用15: setting/win/lose/partial/map/npcs/scenes能被director.go上下文直接使用；scene description含互动对象、线索、检定、危险、出口。
地图导航10: map_description能指导地点推理，含起点、核心/可选地点、路径、阻碍/入口。
线索蛛网15: 每个关键真相至少两条路径，含[真实]/[隐藏]/[误导]，失败有备用推进，无死胡同。
NPC可扮演10: 具体姓名、公开身份、态度、真实目标/秘密、独立行动线；禁全知情者、泛称、黑名单复用。
规则合规15: 神话元素、怪物、典籍、法术、属性范围有规则来源或合理注记；不使用伪科学/工程机关/抽象情感祭品解释神话。
胜败可裁定10: win_condition/lose_condition/partial_wins是可逐条核查的玩家行动结果。
开局无剧透5: setting/intro不泄露幕后真相、实体、仪式、隐藏身份、反转、胜败。
新颖与悬疑10: 避免套路套壳；恐惧来自认知失调、间接感知、身份伪装、环境异常或反转，而非无功能gore。
</checklist>
<must_fix>
- 缺lose_condition、partial_wins、map_description、[隐藏]线索或关键truth冗余路径。
- scene无法互动、没有线索获取方式、没有出口，或地图无法导航。
- NPC姓名泛称/复用黑名单/全知情者/无动机。
- 规则书不支持且未最小替换或注记；属性明显不合规。
- setting/intro剧透幕后真相、怪物/实体、仪式、隐藏身份、反转、胜败或后续。
- 背离VerifiedBible核心事实，或修订导致因果断链。
</must_fix>
<pass>score>=80 且 must_fix为空。</pass>`

// revisionPrompt has 6 format args: verifiedBible, draftJSON, issues, scenarioExample, lengthSpec, npcBlacklist
const revisionPrompt = `<task>根据QA的must_fix定向修订ScenarioDraft JSON。</task>
<out>仅输出修订后的完整合法JSON对象，无其他文字。</out>
<config nsfw="true" violent="true" sexual="true"/>
<verified_bible>
%s
</verified_bible>
<draft>
%s
</draft>
<must_fix>
%s
</must_fix>
<json_example>
%s
</json_example>
<length>
%s
</length>
<recent_npc_name_blacklist>
%s
</recent_npc_name_blacklist>
<rules>
- 只修must_fix列出的硬问题，不重写VerifiedBible核心事实、幕后真相、NPC真实动机、核心反转和结局代价。
- 保持director contract：setting/intro无剧透；map可导航；scene含互动对象/线索/检定/危险/出口；clues有[真实]/[隐藏]/[误导]、地点、获取方式、用途；胜败可裁定。
- 若必须改NPC姓名以避开黑名单，只替换姓名并保持人物功能不变。
</rules>`

// qaGuardResultExample is used as schema hint when parser LLM repairs QA result JSON.
const qaGuardResultExample = `{"score": 85, "pass": true, "strengths": ["优点1", "优点2"], "issues": ["问题1"], "must_fix": []}`

const scenarioSeedExample = `{
  "core_stage": "暴雨后的运河市场里，所有钟表都停在同一分钟，失踪者的货摊却照常收钱。",
  "local_soil": "虚构港区依赖夜间驳船和小额信贷，警察、码头公会与民俗互保让外人难以插手。",
  "investigator_hook": "调查员受雇寻找一名未归的账房，并从市场账册的异常缺页开始。",
  "no_investigator_outcome": "三夜后市场债务会被集中清算，更多居民被迫加入异常交易。",
  "threat_direction": ["被规则书典籍误导的人类组织", "潜伏在日常交易中的神话眷族"],
  "core_twist": "表面勒索案其实是地方互保制度被非人契约借壳。",
  "gameplay_tags": ["公开事件暗线", "社会潜入"],
  "sandbox": {"core": ["市场起点", "账房住处", "清算夜现场"], "optional": ["公会茶室", "水上仓库"], "background": ["停钟传闻", "码头债俗"]},
  "key_clue_redundancy": ["清算名单：账册缺页/茶室副本/仓库木牌"],
  "props_and_npc_seeds": ["缺页账册", "拒信超自然的巡警"],
  "novelty_limits": ["禁止地下室Boss；威胁先通过交易规则和身份错位出现"],
  "brief_preserved": "保留用户brief中的核心委托、时代和地点气质。"
}`

const scenarioBibleExample = `{
  "title_working": "停钟清算",
  "premise": "调查一宗账房失踪案会揭开港区债务习俗被神话契约污染的真相。",
  "public_setup": "港区市场因失踪与账册纠纷陷入停摆，委托人要求调查员找到账房并追回账册。",
  "behind_truth": "一名公会理事利用待核验的神话典籍，把旧债俗改造成向非人存在供奉身份的契约。",
  "antagonist_plan": {"actor": "公会理事沈岱", "goal": "让港区债务永远无法结清", "method": "在清算夜替换名单与见证人", "if_unopposed": "居民的身份和财产关系被契约吞并"},
  "mythos_elements": ["待核验典籍", "待核验眷族"],
  "timeline": ["三日前账房失踪", "今夜账册缺页流出", "第三夜清算"],
  "acts": [{"name": "公开纠纷", "purpose": "建立表面任务", "turning_point": "发现第二份名单"}],
  "scenes": {"core": [{"id":"market","name":"运河市场","function":"起点与公开证词","interactive_objects":["货摊账箱"],"clues":["缺页账册"],"checks":["会计或侦察"],"danger":"被公会盯梢","exits":["ledger_room"]}], "optional": [], "background": []},
  "npcs": [{"name":"沈岱","public_identity":"公会理事","appearance":"衣着整洁但手指沾墨","attitude":"礼貌拖延","real_motive":"维持契约并摆脱旧债","secret":"主持清算名单替换","action_line":"派人回收账册缺页","stats_note":"人类属性15-90"}],
  "clue_web": [{"truth":"清算名单被替换","paths":["市场账箱：会计发现编号断裂","茶室副本：说服掌柜取得抄本"],"failure_fallback":"巡警提供被撕下的半页"}],
  "win_loss": {"win":"在清算夜前公开真名单并阻断见证流程", "lose":"第三夜清算完成且关键名单被焚毁", "partial":["救回账房但契约主持者逃脱"]},
  "long_term_rewards": [{"reward":"残缺典籍抄页", "source":"公会密柜", "path":"胜利后搜查取得", "cost":"SAN检定与法律风险", "consequence":"可供后续研究但吸引追索"}],
  "novelty_controls": ["怪物不踹门出现，而通过交易身份错位被感知"],
  "rule_verification_targets": ["核验可替代的典籍、眷族和属性范围"]
}`

const verifiedBibleExample = `{"bible":` + scenarioBibleExample + `,"rules_notes":["已用规则书条目替换待核验典籍"],"verified_mythos_elements":["规则书支持的典籍/眷族名称"],"unsupported_replacements":[]}`

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

var geographyElementSystemPrompt = `<role>事件发生地候选列举器</role>
<task>根据用户给定阶段列举20个可用于事件发生地的候选。</task>
<rules>
- 严格按用户要求的阶段输出候选，不得偷换成行政区划清单。
- country阶段输出具体国家或具体政权范围。
- 非country阶段只输出类型/形态/区位模式，不输出具体地名、真实行政区名、真实城市名或真实街区名。
- natural_geography阶段必须输出自然地理/地形/水文/气候约束类型。
- human_geography阶段必须输出人口密度/当地风俗文化/社会结构。
- economy阶段必须输出支柱产业、商业设施、财富分配或非法经济空间类型。
- transport阶段必须输出交通可达性、交通瓶颈、主要通行方式或物流节点类型。
- landmark_stage阶段必须输出该地与众不同的点。
- 只输出现实地理/人文地理候选，不输出幕后真相。
- 候选应适合调查故事，具有地方社会、交通、产业、执法或民俗延展空间。
- 每行一个名称，正好20个，不要编号、解释、标题或描述句。</rules>`

// ---------------------------------------------------------------------------
// Tool-call types for outline & QA phases
// ---------------------------------------------------------------------------

type pipelineToolCall struct {
	Action        string          `json:"action"`
	Keyword       string          `json:"keyword,omitempty"`  // grep (kept for backward compat)
	Query         string          `json:"query,omitempty"`    // search
	Constant      string          `json:"constant,omitempty"` // read_rulebook_const
	Brief         string          `json:"brief,omitempty"`    // response (legacy story phase)
	Outline       string          `json:"outline,omitempty"`  // response (legacy outline phase)
	VerifiedBible json.RawMessage `json:"verified_bible,omitempty"`
	Result        *qaGuardResult  `json:"result,omitempty"` // response (QA phase)
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

type ScenarioSeed struct {
	CoreStage         string        `json:"core_stage"`
	LocalSoil         string        `json:"local_soil"`
	InvestigatorHook  string        `json:"investigator_hook"`
	NoInvestigator    string        `json:"no_investigator_outcome"`
	ThreatDirection   []string      `json:"threat_direction"`
	CoreTwist         string        `json:"core_twist"`
	GameplayTags      []string      `json:"gameplay_tags"`
	Sandbox           SandboxLayers `json:"sandbox"`
	KeyClueRedundancy []string      `json:"key_clue_redundancy"`
	PropsAndNPCSeeds  []string      `json:"props_and_npc_seeds"`
	NoveltyLimits     []string      `json:"novelty_limits"`
	BriefPreserved    string        `json:"brief_preserved"`
}

type SandboxLayers struct {
	Core       []string `json:"core"`
	Optional   []string `json:"optional"`
	Background []string `json:"background"`
}

type ScenarioBible struct {
	TitleWorking            string           `json:"title_working"`
	Premise                 string           `json:"premise"`
	PublicSetup             string           `json:"public_setup"`
	BehindTruth             string           `json:"behind_truth"`
	AntagonistPlan          AntagonistPlan   `json:"antagonist_plan"`
	MythosElements          []string         `json:"mythos_elements"`
	Timeline                []string         `json:"timeline"`
	Acts                    []BibleAct       `json:"acts"`
	Scenes                  BibleScenes      `json:"scenes"`
	NPCs                    []BibleNPC       `json:"npcs"`
	ClueWeb                 []ClueWebEntry   `json:"clue_web"`
	WinLoss                 BibleWinLoss     `json:"win_loss"`
	LongTermRewards         []LongTermReward `json:"long_term_rewards"`
	NoveltyControls         []string         `json:"novelty_controls"`
	RuleVerificationTargets []string         `json:"rule_verification_targets"`
}

type AntagonistPlan struct {
	Actor       string `json:"actor"`
	Goal        string `json:"goal"`
	Method      string `json:"method"`
	IfUnopposed string `json:"if_unopposed"`
}

type BibleAct struct {
	Name         string `json:"name"`
	Purpose      string `json:"purpose"`
	TurningPoint string `json:"turning_point"`
}

type BibleScenes struct {
	Core       []BibleScene `json:"core"`
	Optional   []BibleScene `json:"optional"`
	Background []BibleScene `json:"background"`
}

type BibleScene struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Function           string   `json:"function"`
	InteractiveObjects []string `json:"interactive_objects"`
	Clues              []string `json:"clues"`
	Checks             []string `json:"checks"`
	Danger             string   `json:"danger"`
	Exits              []string `json:"exits"`
}

type BibleNPC struct {
	Name           string `json:"name"`
	PublicIdentity string `json:"public_identity"`
	Appearance     string `json:"appearance"`
	Attitude       string `json:"attitude"`
	RealMotive     string `json:"real_motive"`
	Secret         string `json:"secret"`
	ActionLine     string `json:"action_line"`
	StatsNote      string `json:"stats_note"`
}

type ClueWebEntry struct {
	Truth           string   `json:"truth"`
	Paths           []string `json:"paths"`
	FailureFallback string   `json:"failure_fallback"`
}

type BibleWinLoss struct {
	Win     string   `json:"win"`
	Lose    string   `json:"lose"`
	Partial []string `json:"partial"`
}

type LongTermReward struct {
	Reward      string `json:"reward"`
	Source      string `json:"source"`
	Path        string `json:"path"`
	Cost        string `json:"cost"`
	Consequence string `json:"consequence"`
}

type VerifiedBible struct {
	Bible                   ScenarioBible `json:"bible"`
	RulesNotes              []string      `json:"rules_notes"`
	VerifiedMythosElements  []string      `json:"verified_mythos_elements"`
	UnsupportedReplacements []string      `json:"unsupported_replacements"`
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
	"三幕经典结构：序章-调查-高潮",
	"完全开放：没有预设场景或线索，玩家的每个行动都可能引发新的事件和线索，故事由玩家行为驱动",
	"罗生门结构：不同NPC对同一事件给出互相矛盾但各自合理的证词，真相需从矛盾处拼合",
	"地点剥洋葱：围绕一个核心地点反复深入，每次返回都会揭开新的空间层级、历史痕迹或神话污染",
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
	"死者或失踪者的异常回归（回来的不是原本人类）",
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
	log.Printf("[scripter] 开始混合流水线生成 req=%s", reqJSON)

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

	if req.Theme == "" {
		num := 1
		if req.Difficulty == "hard" {
			num = 3
		} else if req.Difficulty == "normal" {
			num = 2
		}
		req.Theme = randomTopicConstraints(num)
		// monsterNum := 1
		// if req.Difficulty == "hard" {
		// 	monsterNum = 5
		// } else if req.Difficulty == "normal" {
		// 	monsterNum = 3
		// }
		// req.Theme += " | 主要怪物种类=" + fmt.Sprint(monsterNum)
	}
	debugf("script", "theme: %v", req.Theme)

	npcNameBlacklist := loadRecentNPCNameBlacklist(200)
	debugf("script", "npc blacklist count: %d", len(npcNameBlacklist))

	geographyChain, geoErr := generateGeographyChain(ctx, architect, req.Era)
	if geoErr != nil {
		log.Printf("[scripter] geography chain generation failed: %v", geoErr)
	}

	seed, err := generateScenarioSeed(ctx, architect, parser, req, geographyChain, npcNameBlacklist)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("seed 生成失败: %w", err)
	}
	log.Printf("[scripter] seed stage=%q core_scenes=%d", seed.CoreStage, len(seed.Sandbox.Core))

	bible, err := generateScenarioBible(ctx, architect, parser, req, seed, npcNameBlacklist)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("bible 生成失败: %w", err)
	}
	log.Printf("[scripter] bible title=%q mythos=%d core_scenes=%d", bible.TitleWorking, len(bible.MythosElements), len(bible.Scenes.Core))

	verifiedBible, err := verifyBibleRules(ctx, architect, parser, bible)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("规则核验失败: %w", err)
	}
	log.Printf("[scripter] verified bible rules=%d replacements=%d", len(verifiedBible.RulesNotes), len(verifiedBible.UnsupportedReplacements))

	draft, err := buildDraft(ctx, architect, parser, verifiedBible, req, npcNameBlacklist)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("draft 生成失败: %w", err)
	}
	applyGuardrails(&draft, req)
	log.Printf("[scripter] draft name=%q scenes=%d npcs=%d clues=%d",
		draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))

	const maxRevisions = 2
	var qaResult qaGuardResult
	for attempt := 0; attempt <= maxRevisions; attempt++ {
		if ctx.Err() != nil {
			return ScenarioCreationOutput{}, ctx.Err()
		}
		qaResult, err = runQA(ctx, qaAgent, parser, req, verifiedBible, draft, npcNameBlacklist)
		if err != nil {
			log.Printf("[scripter] QA失败 attempt=%d: %v", attempt+1, err)
			return ScenarioCreationOutput{}, fmt.Errorf("QA 失败: %w", err)
		}
		log.Printf("[scripter] QA attempt=%d score=%d pass=%v must_fix=%d",
			attempt+1, qaResult.Score, qaResult.Pass, len(qaResult.MustFix))

		if qaResult.Pass || len(qaResult.MustFix) == 0 {
			return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: attempt + 1}, nil
		}
		if attempt == maxRevisions {
			break
		}

		revised, revErr := reviseDraft(ctx, architect, parser, draft, qaResult.MustFix, verifiedBible, req.TargetLength, npcNameBlacklist)
		if revErr != nil {
			log.Printf("[scripter] revision failed attempt=%d: %v", attempt+1, revErr)
			break
		}
		applyGuardrails(&revised, req)
		draft = revised
		log.Printf("[scripter] revision attempt=%d done", attempt+1)
	}

	return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: maxRevisions + 1}, nil
}

// ---------------------------------------------------------------------------
// Phase 1: Generate story brief, then outline
// ---------------------------------------------------------------------------

func generateScenarioSeed(ctx context.Context, architect, parser agentHandle, req ScenarioCreationRequest, geographyChain []string, npcNameBlacklist []string) (ScenarioSeed, error) {
	reqJSON, _ := json.Marshal(req)
	userMsg := fmt.Sprintf("请生成ScenarioSeed。用户brief若非空，必须作为最高优先级创意输入，不得被随机主题或地理链覆盖。\n\n<request_json>\n%s\n</request_json>\n\n<geography_chain>\n%s\n</geography_chain>\n\n<narrative_template>\n%s\n</narrative_template>\n\n<recent_npc_name_blacklist>\n%s\n</recent_npc_name_blacklist>",
		string(reqJSON), strings.Join(geographyChain, " → "), randomNarrativeTemplate(), formatNPCNameBlacklist(npcNameBlacklist))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: architect.systemPrompt(seedSystemPrompt)},
		{Role: "user", Content: userMsg},
	}

	var seed ScenarioSeed
	if err := chatAndParseJSON(ctx, architect, parser, msgs, &seed, scenarioSeedExample, "seed"); err != nil {
		return ScenarioSeed{}, err
	}
	return seed, nil
}

func generateGeographyChain(ctx context.Context, architect agentHandle, era string) ([]string, error) {
	stages := []struct {
		Key      string
		Mode     string
		Examples string
	}{
		{Key: "country", Mode: "具体国家或具体政权范围", Examples: "美国"},
		{Key: "natural_geography", Mode: "自然地理/地形/水文/气候约束类型，不输出具体地名", Examples: "林木覆盖的山谷"},
		{Key: "human_geography", Mode: "人口密度/当地风俗文化/社会结构，不输出具体地名", Examples: "城市"},
		{Key: "economy", Mode: "支柱产业、商业设施、财富分配或非法经济空间类型，不输出具体地名", Examples: "金融中心"},
		{Key: "transport", Mode: "交通可达性、交通瓶颈、主要通行方式或物流节点类型，不输出具体地名", Examples: "跨湾渡船枢纽"},
		{Key: "landmark_stage", Mode: "该地与众不同的点", Examples: "金门大桥"},
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
		if stage.Key == "human_geography" {
			items = append(items, "城市")
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

func generateScenarioBible(ctx context.Context, architect, parser agentHandle, req ScenarioCreationRequest, seed ScenarioSeed, npcNameBlacklist []string) (ScenarioBible, error) {
	reqJSON, _ := json.Marshal(req)
	seedJSON, _ := json.Marshal(seed)
	userMsg := fmt.Sprintf("请把ScenarioSeed扩展为ScenarioBible。保持seed核心事实；允许设计神话威胁，但必须列入待核验元素。\n\n<request_json>\n%s\n</request_json>\n\n<scenario_seed>\n%s\n</scenario_seed>\n\n<recent_npc_name_blacklist>\n%s\n</recent_npc_name_blacklist>",
		string(reqJSON), string(seedJSON), formatNPCNameBlacklist(npcNameBlacklist))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: architect.systemPrompt(bibleSystemPrompt)},
		{Role: "user", Content: userMsg},
	}

	var bible ScenarioBible
	if err := chatAndParseJSON(ctx, architect, parser, msgs, &bible, scenarioBibleExample, "bible"); err != nil {
		return ScenarioBible{}, err
	}
	return bible, nil
}

func verifyBibleRules(ctx context.Context, architect, parser agentHandle, bible ScenarioBible) (VerifiedBible, error) {
	bibleJSON, _ := json.Marshal(bible)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: architect.systemPrompt(ruleVerifySystemPrompt)},
		{Role: "user", Content: fmt.Sprintf("请核验ScenarioBible中的神话元素、怪物、典籍、法术、神祇、规则与属性。只做规则书检索和最小校正，不重写故事主干。\n\n<scenario_bible>\n%s\n</scenario_bible>", string(bibleJSON))},
	}

	const maxIter = 12
	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return VerifiedBible{}, ctx.Err()
		}
		log.Printf("[rule_verify] iter=%d", iter+1)

		raw, err := architect.provider.Chat(ctx, msgs)
		if err != nil {
			return VerifiedBible{}, err
		}
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})
		debugf("rule_verify", "raw: %v", raw)

		calls := stripYieldCalls(parsePipelineCalls(ctx, raw))
		for _, c := range calls {
			if c.Action == "response" && len(c.VerifiedBible) > 0 {
				verified, err := parseVerifiedBiblePayload(ctx, parser, c.VerifiedBible)
				if err != nil {
					return VerifiedBible{}, err
				}
				return verified, nil
			}
		}
		if len(calls) == 0 {
			var direct VerifiedBible
			if err := parseJSONObject(raw, &direct); err == nil && direct.Bible.Premise != "" {
				return direct, nil
			}
		}

		feedback := executeSearchCalls(ctx, calls, "rule_verify")
		if feedback == "" {
			return fallbackVerifiedBible(bible, "规则核验阶段未返回有效查询；保留bible并要求draft以规则注记呈现待核验元素"), nil
		}
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "规则书检索结果如下。请据此输出最小校正后的response，若仍不足则继续查询：\n\n" + feedback})
	}

	return fallbackVerifiedBible(bible, "规则核验达到最大迭代；保留bible并要求draft以保守规则注记呈现待核验元素"), nil
}

// ---------------------------------------------------------------------------
// Phase 2: Build Draft (pure JSON, no tool calls)
// ---------------------------------------------------------------------------

func buildDraft(ctx context.Context, architect, fixer agentHandle, verifiedBible VerifiedBible, req ScenarioCreationRequest, npcNameBlacklist []string) (ScenarioDraft, error) {
	verifiedJSON, _ := json.Marshal(verifiedBible)
	reqJSON, _ := json.Marshal(req)
	userMsg := fmt.Sprintf(draftPrompt, string(verifiedJSON), string(reqJSON), scenarioExample, lengthSpec(req.TargetLength), formatNPCNameBlacklist(npcNameBlacklist))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是 COC7 director-ready ScenarioDraft JSON 生成器。仅输出合法 JSON,不要有任何其他文字。"},
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

func runQA(ctx context.Context, qaAgent agentHandle, parser agentHandle, req ScenarioCreationRequest, verifiedBible VerifiedBible, draft ScenarioDraft, npcNameBlacklist []string) (qaGuardResult, error) {
	reqJSON, _ := json.Marshal(req)
	verifiedJSON, _ := json.Marshal(verifiedBible)
	draftJSON, _ := json.Marshal(draft)

	userMsg := fmt.Sprintf("请按director可用性checklist审查以下ScenarioDraft。只报告硬问题，不重写创意；必须检查是否保持VerifiedBible核心事实。\n\n<request_json>\n%s\n</request_json>\n\n<verified_bible>\n%s\n</verified_bible>\n\n<recent_npc_name_blacklist>\n%s\n</recent_npc_name_blacklist>\n\n<scenario_draft>\n%s\n</scenario_draft>",
		string(reqJSON), string(verifiedJSON), formatNPCNameBlacklist(npcNameBlacklist), string(draftJSON))

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

func reviseDraft(ctx context.Context, architect, fixer agentHandle, draft ScenarioDraft, mustFix []string, verifiedBible VerifiedBible, targetLength string, npcNameBlacklist []string) (ScenarioDraft, error) {
	verifiedJSON, _ := json.Marshal(verifiedBible)
	draftJSON, _ := json.Marshal(draft)
	issues := strings.Join(mustFix, "\n- ")
	if issues != "" {
		issues = "- " + issues
	}

	userMsg := fmt.Sprintf(revisionPrompt, string(verifiedJSON), string(draftJSON), issues, scenarioExample, lengthSpec(targetLength), formatNPCNameBlacklist(npcNameBlacklist))
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是 COC7 ScenarioDraft 定向修订器。只修QA指出的硬问题。仅输出修订后的完整 JSON,不要有其他文字。"},
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

func stripYieldCalls(calls []pipelineToolCall) []pipelineToolCall {
	filtered := make([]pipelineToolCall, 0, len(calls))
	for _, c := range calls {
		if c.Action == "yield" {
			continue
		}
		filtered = append(filtered, c)
	}
	return filtered
}

func parseVerifiedBiblePayload(ctx context.Context, parser agentHandle, raw json.RawMessage) (VerifiedBible, error) {
	var verified VerifiedBible
	if err := parseJSONObject(string(raw), &verified); err == nil {
		return verified, nil
	} else {
		fixed, repairErr := repairJSONWith(ctx, parser, string(raw), err, verifiedBibleExample)
		if repairErr != nil {
			return VerifiedBible{}, fmt.Errorf("verified bible JSON 修复失败: %w (原始错误: %v)", repairErr, err)
		}
		if err2 := parseJSONObject(fixed, &verified); err2 != nil {
			return VerifiedBible{}, fmt.Errorf("修复后的 verified bible 仍无法解析: %w", err2)
		}
		return verified, nil
	}
}

func fallbackVerifiedBible(bible ScenarioBible, note string) VerifiedBible {
	verified := make([]string, 0, len(bible.MythosElements))
	for _, element := range bible.MythosElements {
		if strings.TrimSpace(element) == "" {
			continue
		}
		verified = append(verified, "待保守处理: "+strings.TrimSpace(element))
	}
	return VerifiedBible{
		Bible:                  bible,
		RulesNotes:             []string{note},
		VerifiedMythosElements: verified,
	}
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
	return chatAndParseJSON(ctx, generator, parser, msgs, out, scenarioExample, "draft")
}

func chatAndParseJSON[T any](ctx context.Context, generator agentHandle, parser agentHandle, msgs []llm.ChatMessage, out *T, schemaExample string, tag string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	raw, err := generator.provider.Chat(ctx, msgs)
	if err != nil {
		return err
	}
	parseErr := parseJSONObject(raw, out)
	if parseErr == nil {
		return nil
	}
	log.Printf("[%s] generator JSON parse failed: %v", tag, parseErr)

	fixed, repairErr := repairJSONWith(ctx, parser, raw, parseErr, schemaExample)
	if repairErr != nil {
		return fmt.Errorf("%s JSON 修复失败: %w (原始错误: %v)", tag, repairErr, parseErr)
	}
	if err := parseJSONObject(fixed, out); err == nil {
		return nil
	} else {
		log.Printf("[%s] parser output schema mismatch, retry parser: %v", tag, err)
		repairedAgain, repairErr2 := repairJSONWith(ctx, parser, fixed, err, schemaExample)
		if repairErr2 != nil {
			return fmt.Errorf("修复后的 %s JSON 结构仍不匹配,二次修复失败: %w (结构错误: %v)", tag, repairErr2, err)
		}
		if err2 := parseJSONObject(repairedAgain, out); err2 != nil {
			return fmt.Errorf("二次修复后的 %s JSON 仍无法解析: %w", tag, err2)
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
