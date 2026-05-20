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

var seedSystemPrompt = `<role>COC7单页模组敏捷设计师</role>
<task>严格按“单页模组敏捷开发”五步输出ScenarioSeed：1选择冲突舞台，2撰写核心故事线，3划定同心圆沙盒，4连接线索，5填充NPC和证据。只做种子，不写完整剧本。</task>
<config nsfw="true" violent="true" sexual="true"/>
<output>只输出合法JSON对象，字段必须匹配ScenarioSeed；不要Markdown、标题、解释或代码围栏。</output>
<priority>
- 如果用户brief非空，必须作为最高优先级创意输入吸收并保留核心意图，不得被地理背景覆盖。
- 地理链只用于补强五步内容，不得替代五步结构。
</priority>
<schema>
{
  "situation": {
    "crime_scene": "第一步：核心局面/犯罪现场/最有张力的舞台。它是冒险围绕展开的具体场景，不是剧情梗概，也不是A/B理论说明。",
    "visual_anchor": "这个舞台最直观的紧张画面或现场细节。",
    "why_interesting": "为什么这个局面值得围绕它设计冒险。"
  },
  "plot": {
    "background_investigation": "第二步-背景调查：这个可怕地方背后有什么故事。",
    "antagonist_birth": "第二步-反派诞生：坏人/怪物/威胁如何产生或为什么开始行动。",
    "antagonist_goal": "如果坏人或怪物有意志，它的目标是什么。",
    "if_unopposed": "第二步-事件推演：如果没有调查员，事情会如何发展。",
    "investigator_entry": "第二步-英雄入场：玩家将在哪里、以什么方式卷入。"
  },
  "sandbox": {
    "core": [{"name":"核心区域场景","event":"必须发生什么","why_required":"为什么没有这个场景故事无法推进到结局","state_change":"场景结束后故事状态如何改变"}],
    "middle": [{"name":"中间区域/可选探索","reward":"额外信息或挑战","connects_to_core":"如何帮助进入或理解核心区域"}],
    "outer": [{"name":"最外层背景幕","function":"地名、传说或概念如何增加真实感"}]
  },
  "clue_connections": [{"key_information":"关键情报","paths":[{"source":"线索来源","method":"获取方式/检定/交涉","scene_layer":"core|middle|outer"}]}],
  "wrap_up": {
    "npcs": [{"name_or_role":"NPC姓名或角色种子","personality_trait":"鲜明人格特征","function":"在场景、线索或冲突中的作用"}],
    "evidence_props": [{"name":"证据/实物道具","player_facing_detail":"玩家能直接看到或读到的信息","function":"连接哪条线索或场景"}]
  },
  "novelty_limits": ["必须避免的套路或需要重塑的常见结构"],
  "brief_preserved": "用户brief的核心意图如何被保留"
}
</schema>
<rules>
- situation.crime_scene 对应第一步“选择冲突舞台”：只锁定一个最适合展开调查的核心现场/局面；不要把完整剧情、调查员入场、结局、神话解释塞进去。
- plot 必须逐项回答第二步四个问题：背景调查、反派诞生/目标、调查员不来会怎样、玩家如何卷入。
- sandbox 必须对应第三步同心圆：core=故事的锁/必须发生的核心场景；middle=非强制但提供额外信息或挑战；outer=背景幕。
- sandbox.core 必须是3-5个核心场景节点。每个节点都要写清event、why_required、state_change；禁止只写地点名、氛围名或可选支线。
- clue_connections 必须对应第四步：每个关键情报至少两条获取路径，路径可来自core/middle/outer，但必须最终帮助推进核心区域。
- wrap_up 必须对应第五步：NPC要有鲜明人格和功能；evidence_props要是玩家可见、可引用的证据或handout种子。
- 可以提出神话威胁方向，但只作为plot中的威胁种子，不做规则裁定；规则核验留给后续阶段。
- 禁默认套路堆叠：孤岛、灯塔、渔村、旧宅、失踪教授、邪教仪式、梦境真相、古书召唤、地下室Boss。若输入强制使用，必须在novelty_limits写出重塑方式。
- 全阶段禁止使用伪科学解释异常或神话：不得用科学术语、机械装置、电子设备、声波、振动、频率、传感器、药剂、催眠器、信号标记、安全区、能量场、量子、磁场、电磁波、生物实验、心理暗示等解释神话现象或作为解决神话的关键。若需要神话来源，只能来自规则书支持的神话实体、典籍、法术、物品或清晰的超自然规则注记。
- 禁宏大工程、国家级设施、军事/核能/航天/深海/高能物理/绝密研究。
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
  "scenes": {"core": [{"id":"稳定英文或拼音id","name":"核心场景名","function":"锁扣功能：入场/筛选/转折/高潮/结局之一","interactive_objects":["可互动对象"],"clues":["核心线索或关键事件"],"checks":["检定/代价"],"danger":"危险","exits":["由本节点因果解锁的下一个核心场景id"]}], "optional": [], "background": []},
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
- 核心事实必须承接seed五步：situation.crime_scene/visual_anchor、plot中的背景/威胁目标/无人介入结果/调查员入场、sandbox三层、clue_connections和wrap_up不能无故改写。
- scenes.core 必须是3-5个“锁扣节点”，每个节点都要满足：不可跳过、推进主线、包含关键信息/事件、与前后核心节点有逻辑因果。禁止把核心场景写成地点清单或可任意调换顺序的独立事件。
- scenes.core 的exits必须体现因果解锁关系：场景A结束后，故事状态如何变化，为什么场景B才会发生。若某场景可绕过或只补充背景，放入optional/background。
- 每个关键情报至少两条获取路径；至少一条非运气推理胜利路径。
- NPC必须可扮演：具体姓名、公开表象、态度、真实目标/秘密、独立行动线；禁主要NPC全知情者。
- 长期奖励必须来自故事直接后果，写清路径、来源、代价、风险、后果，禁无条件白送。
- 全阶段禁止使用伪科学解释异常或神话：不得用科学术语、机械装置、电子设备、声波、振动、频率、传感器、药剂、催眠器、信号标记、安全区、能量场、量子、磁场、电磁波、生物实验、心理暗示等解释神话现象或作为解决神话的关键。
- 禁工程机关/抽象情感祭品解释神话；禁空泛神秘词和奇幻小说化。
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
- 若ScenarioBible使用伪科学解释异常或神话，必须做最小替换：删除科学术语、机械装置、电子设备、声波、振动、频率、传感器、药剂、催眠器、信号标记、安全区、能量场、量子、磁场、电磁波、生物实验、心理暗示等解释，并改为规则书支持的神话实体、典籍、法术、物品或清晰的超自然规则注记。
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
<ban>全阶段禁止使用伪科学解释异常或神话：不得用科学术语、机械装置、电子设备、声波、振动、频率、传感器、药剂、催眠器、信号标记、安全区、能量场、量子、磁场、电磁波、生物实验、心理暗示等解释神话现象或作为解决神话的关键；抽象情感/象征祭品作锚点/钥匙/封印/唯一解法；空泛神秘词；主要NPC全知情。</ban>
`

var qaSystemPrompt = `<role>COC7 director可用性QA</role>
<task>只审查ScenarioDraft的字段完整性、director可直接运行性、规则合规、线索格式、开局无剧透；不重写创意。</task>
<config nsfw="true" violent="true" sexual="true"/>
<rulebook>` + rulebook.RulebookDir + `</rulebook>
<tools>
search:{"action":"search","query":"自然语言规则查询"}
read_rulebook_const:{"action":"read_rulebook_const","constant":"rulebook_dir|rulebook_detail_dir|aliens|books|great_old_ones_and_gods|monsters|mythos_creatures|spells"}
response:{"action":"response","result":{"score":N,"pass":bool,"strengths":[...],"issues":[...],"must_fix":[...]}}
</tools>
<exec>
- 只允许输出单个JSON数组，数组元素只能是search/read_rulebook_const/response。
- 若草案包含怪物/法术/典籍/神话来源且VerifiedBible没有足够规则注记，先输出read_rulebook_const或search进行核实；查询轮禁止response。
- 若无需规则书查询，或收到规则书搜索结果后信息足够，必须只输出1个response action；response不得和查询混用。
- 若信息仍不足，继续输出至少1个有效查询；禁止空数组、yield、自然语言、Markdown。
</exec>
<checklist total="100">
字段完整20: ScenarioDraft顶层字段和ScenarioContent字段齐全，scenes/npcs/clues数量符合target_length。
Director可用20: content.setting、win_condition、lose_condition、partial_wins、map_description、npcs、scenes字段均可直接注入KP上下文；scene description包含互动对象、线索、检定、危险、出口这些可运行要素。
地图导航10: map_description包含起点、核心/可选地点、路径、阻碍/入口、可回退路线。
线索格式15: clues[]含[真实]/[隐藏]/[误导]前缀；每条写明名称、地点、获取方式、用途；关键情报有冗余路径字段或文本说明。
NPC字段10: NPC有具体姓名、公开身份/描述、态度、stats；禁止职业/身份泛称和黑名单姓名复用。
规则合规15: 神话元素、怪物、典籍、法术、属性范围有规则来源或合理注记；全阶段禁止使用伪科学解释异常或神话，不得用科学术语、机械装置、电子设备、声波、振动、频率、传感器、药剂、催眠器、信号标记、安全区、能量场、量子、磁场、电磁波、生物实验、心理暗示等解释神话现象或作为解决神话的关键。
开局无剧透5: setting/intro不透露幕后真相、怪物/实体、仪式、隐藏身份、反转、胜败或后续剧情。
胜败字段5: win_condition/lose_condition/partial_wins存在且是可被KP记录的条件文本。
</checklist>
<must_fix>
- 缺少必需字段：lose_condition、partial_wins、map_description、scenes、npcs、clues等。
- scenes缺少互动对象、线索获取方式、检定、危险或出口等运行要素。
- map_description缺少起点、地点关系、路径、阻碍/入口或可回退路线。
- clues缺少[真实]/[隐藏]/[误导]前缀、地点、获取方式或用途；关键情报没有冗余路径说明。
- NPC姓名泛称、复用黑名单、缺少公开身份/描述/态度/stats。
- 规则书不支持且未最小替换或注记；属性明显不合规。
- 使用伪科学解释异常或神话，或用科学术语、机械装置、电子设备、声波、振动、频率、传感器、药剂、催眠器、信号标记、安全区、能量场、量子、磁场、电磁波、生物实验、心理暗示等作为解决神话的关键。
- setting/intro剧透幕后真相、怪物/实体、仪式、隐藏身份、反转、胜败或后续。
</must_fix>
<pass>score>=80 且 must_fix为空</pass>`

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
- 只修must_fix列出的字段、格式、规则、开局剧透和director运行性硬问题；不要借修订重写剧情创意。
- 不修剧情逻辑、人物动机、因果链、制度流程或期限是否成立。
- 保持director contract：setting/intro无剧透；map可导航；scene含互动对象/线索/检定/危险/出口；clues有[真实]/[隐藏]/[误导]、地点、获取方式、用途；胜败字段可记录。
- 修订后仍必须禁止伪科学解释异常或神话；不得引入科学术语、机械装置、电子设备、声波、振动、频率、传感器、药剂、催眠器、信号标记、安全区、能量场、量子、磁场、电磁波、生物实验、心理暗示等作为神话解释或解决关键。
- 若必须改NPC姓名以避开黑名单，只替换姓名并保持人物功能不变。
</rules>`

// qaGuardResultExample is used as schema hint when parser LLM repairs QA result JSON.
const qaGuardResultExample = `{"score": 85, "pass": true, "strengths": ["优点1", "优点2"], "issues": ["问题1"], "must_fix": []}`

const scenarioSeedExample = `{
  "situation": {
    "crime_scene": "1920年代城市拍卖行预展厅，博物馆要在闭馆前拍出一只失踪考察队托运回来的标本箱来偿还债务，考察队家属要求撤拍，因为托运单上的签名日期晚于全队失踪日。",
    "visual_anchor": "封蜡未拆的标本箱摆在拍卖台中央，拍卖师守着落槌时间，家属拿着失踪剪报堵住买家入口。",
    "why_interesting": "同一个标本箱同时是博物馆的还债资产、家属追查失踪的证据、买家的竞拍目标；调查天然围绕托运单、箱内物和拍卖流程展开。"
  },
  "plot": {
    "background_investigation": "博物馆为资助考察欠下债务，考察队失踪后只有一批标本箱按期抵达，董事会决定拍卖其中一箱止损。",
    "antagonist_birth": "策展人发现箱内物会吸引特定买家，开始伪造托运文件以便绕过家属和保险公司的追索，背后神话来源留待规则核验。",
    "antagonist_goal": "在所有权争议成立前完成落槌，把标本箱交给指定买家，并让失踪者在文书上变成自愿托运人。",
    "if_unopposed": "拍卖完成后，标本箱离开公开视野，失踪案证据链断裂，箱内威胁进入城市私人收藏圈。",
    "investigator_entry": "调查员受家属、保险公司或博物馆董事委托，在闭馆拍卖前核对托运单、拍品来源和考察队最后行踪。"
  },
  "sandbox": {
    "core": [
      {"name":"预展厅争执","event":"家属、博物馆和买家围绕标本箱是否撤拍爆发公开争执","why_required":"没有这个场景就没有共同争夺物、期限和调查入口","state_change":"确认关键问题是托运单日期、标本箱所有权和闭馆前落槌"},
      {"name":"档案核对","event":"调查员取得拍品登记、托运单或保险文件中的日期矛盾","why_required":"没有文件矛盾就无法挑战拍卖合法性","state_change":"拍卖纠纷升级为伪造文书和失踪案线索"},
      {"name":"标本箱查验","event":"调查员检查封蜡、气味、标签或箱内物，发现它与考察队失踪直接相关","why_required":"没有查验就无法把文书疑点推进到幕后真相","state_change":"解锁阻止落槌、追踪买家或公开证据的结局选择"}
    ],
    "middle": [
      {"name":"装卸间","reward":"搬运工记得箱子曾在夜里被调换位置","connects_to_core":"提供托运单之外的第二条箱体异常路径"},
      {"name":"董事办公室","reward":"债务函和指定买家的私人便条","connects_to_core":"说明为什么博物馆急于闭馆前完成拍卖"}
    ],
    "outer": [
      {"name":"城市收藏圈传闻","function":"说明为什么某些买家愿意为未开封标本箱支付异常高价"}
    ]
  },
  "clue_connections": [
    {"key_information":"托运签名晚于考察队失踪日","paths":[{"source":"拍卖行托运单原件","method":"法律/会计/侦察检定","scene_layer":"core"},{"source":"保险公司副本","method":"说服保险代理或查档","scene_layer":"middle"}]},
    {"key_information":"标本箱在抵达后被人调换或重新封蜡","paths":[{"source":"标本箱封蜡和标签","method":"侦察/手艺/博物学检定","scene_layer":"core"},{"source":"装卸间搬运工证词","method":"交涉或信用评级","scene_layer":"middle"}]}
  ],
  "wrap_up": {
    "npcs": [
      {"name_or_role":"策展人","personality_trait":"镇定、爱用程序和债务压人","function":"推动拍卖并掌握拍品来源文件"},
      {"name_or_role":"考察队家属代表","personality_trait":"克制但拒绝让箱子离场","function":"提供失踪时间线和情感压力"},
      {"name_or_role":"沉默买家代理","personality_trait":"不看展品，只盯落槌时间","function":"制造倒计时和后续追踪目标"}
    ],
    "evidence_props": [
      {"name":"托运单与保险副本","player_facing_detail":"两份文件的签名相似，但日期和承运印章不一致","function":"连接预展厅、档案核对和最终是否阻止拍卖"},
      {"name":"标本箱封蜡照片","player_facing_detail":"封蜡边缘夹着不属于博物馆的深色纤维","function":"连接标本箱查验和装卸间证词"}
    ]
  },
  "novelty_limits": ["不使用失踪教授、地下室Boss或邪教仪式；神话威胁必须从拍品来源、收藏圈和所有权文书中长出"],
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

var geographyElementSystemPrompt = `<role>事件发生地候选列举器</role>
<task>根据用户给定阶段列举20个可用于事件发生地的候选。</task>
<rules>
- 严格按用户要求的阶段输出候选，不得偷换成行政区划清单。
- country阶段输出具体国家或具体政权范围。
- 非country阶段只输出类型/形态/区位模式，不输出具体地名、真实行政区名、真实城市名或真实街区名。
- natural_geography阶段必须输出自然地理/地形/水文/气候约束类型。
- human_geography阶段必须输出人口密度/当地风俗文化/社会结构。
- 只输出现实地理/人文地理候选，不输出幕后真相。
- 禁止输出伪科学、高科技、工程化异常或可诱导伪科学解释神话的候选。
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
	Situation       SeedSituation        `json:"situation"`
	Plot            SeedPlot             `json:"plot"`
	Sandbox         SeedSandbox          `json:"sandbox"`
	ClueConnections []SeedClueConnection `json:"clue_connections"`
	WrapUp          SeedWrapUp           `json:"wrap_up"`
	NoveltyLimits   []string             `json:"novelty_limits"`
	BriefPreserved  string               `json:"brief_preserved"`
}

type SeedSituation struct {
	CrimeScene     string `json:"crime_scene"`
	VisualAnchor   string `json:"visual_anchor"`
	WhyInteresting string `json:"why_interesting"`
}

type SeedPlot struct {
	BackgroundInvestigation string `json:"background_investigation"`
	AntagonistBirth         string `json:"antagonist_birth"`
	AntagonistGoal          string `json:"antagonist_goal"`
	IfUnopposed             string `json:"if_unopposed"`
	InvestigatorEntry       string `json:"investigator_entry"`
}

type SeedSandbox struct {
	Core   []SeedCoreScene       `json:"core"`
	Middle []SeedMiddleScene     `json:"middle"`
	Outer  []SeedBackgroundLayer `json:"outer"`
}

type SeedCoreScene struct {
	Name        string `json:"name"`
	Event       string `json:"event"`
	WhyRequired string `json:"why_required"`
	StateChange string `json:"state_change"`
}

type SeedMiddleScene struct {
	Name           string `json:"name"`
	Reward         string `json:"reward"`
	ConnectsToCore string `json:"connects_to_core"`
}

type SeedBackgroundLayer struct {
	Name     string `json:"name"`
	Function string `json:"function"`
}

type SeedClueConnection struct {
	KeyInformation string         `json:"key_information"`
	Paths          []SeedCluePath `json:"paths"`
}

type SeedCluePath struct {
	Source     string `json:"source"`
	Method     string `json:"method"`
	SceneLayer string `json:"scene_layer"`
}

type SeedWrapUp struct {
	NPCs          []SeedNPCSeed      `json:"npcs"`
	EvidenceProps []SeedEvidenceProp `json:"evidence_props"`
}

type SeedNPCSeed struct {
	NameOrRole       string `json:"name_or_role"`
	PersonalityTrait string `json:"personality_trait"`
	Function         string `json:"function"`
}

type SeedEvidenceProp struct {
	Name               string `json:"name"`
	PlayerFacingDetail string `json:"player_facing_detail"`
	Function           string `json:"function"`
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

type scripterToolCall struct {
	Action     string         `json:"action"`
	Think      string         `json:"think,omitempty"`
	Question   string         `json:"question,omitempty"`
	Constant   string         `json:"constant,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Review     *agentReview   `json:"review,omitempty"`
	Result     *qaGuardResult `json:"result,omitempty"`
	Background *FogBackground `json:"background,omitempty"`
	Gameplay   *GameplayFrame `json:"gameplay_frame,omitempty"`
	Truth      *TruthStack    `json:"truth,omitempty"`
	Maze       *ClueMaze      `json:"maze,omitempty"`
	Cast       *CastPlan      `json:"cast,omitempty"`
	Draft      *ScenarioDraft `json:"draft,omitempty"`
}

type scripterStageSpec struct {
	Name             string
	SystemPrompt     string
	UserPrompt       string
	ExpectedPayload  string
	MaxTurns         int
	RequireRuleCheck bool
	SchemaExample    string
}

type scripterRoom struct {
	architect      agentHandle
	qa             agentHandle
	parser         agentHandle
	req            ScenarioCreationRequest
	geographyChain []string
	npcBlacklist   []string
	titleSamples   []string
}

type virtualScripterAgent struct {
	name   string
	handle agentHandle
	system string
}

type scripterAgents struct {
	SettingScout       virtualScripterAgent
	GameplayGrounder   virtualScripterAgent
	MysteryArchitect   virtualScripterAgent
	MisdirectionCritic virtualScripterAgent
	ClueCartographer   virtualScripterAgent
	PlayerSimulator    virtualScripterAgent
	CastDramatist      virtualScripterAgent
	ContinuityEditor   virtualScripterAgent
	ScenarioAssembler  virtualScripterAgent
	FinalQAGuard       virtualScripterAgent
}

type agentReview struct {
	Pass          bool     `json:"pass"`
	Issues        []string `json:"issues"`
	RevisionBrief string   `json:"revision_brief"`
}

type FogBackground struct {
	TimeAndPlace       string   `json:"time_and_place"`
	InvestigatorHook   string   `json:"investigator_hook"`
	DailyBeauty        string   `json:"daily_beauty"`
	UnsettlingDetail   string   `json:"unsettling_detail"`
	PublicProblem      string   `json:"public_problem"`
	BriefPreserved     string   `json:"brief_preserved"`
	AntiTropeExecution []string `json:"anti_trope_execution"`
}

type GameplayFrame struct {
	Gameplay             string   `json:"gameplay"`
	FitReason            string   `json:"fit_reason"`
	BackgroundDerivation []string `json:"background_derivation"`
	InvestigationLoop    string   `json:"investigation_loop"`
	PlayerActions        []string `json:"player_actions"`
	FrictionPoints       []string `json:"friction_points"`
	FailurePressure      string   `json:"failure_pressure"`
	RuleTouchpoints      []string `json:"rule_touchpoints"`
	Boundaries           []string `json:"boundaries"`
}

type TruthStack struct {
	AppearanceBelief        string   `json:"appearance_belief"`
	AppearanceEvidence      []string `json:"appearance_evidence"`
	SurfaceTruth            string   `json:"surface_truth"`
	SurfaceExplainsEvidence []string `json:"surface_explains_evidence"`
	WhyVeteransStopHere     []string `json:"why_veterans_stop_here"`
	DeepTruth               string   `json:"deep_truth"`
	DeepTruthAccessCosts    []string `json:"deep_truth_access_costs"`
	MythosElements          []string `json:"mythos_elements"`
}

type ClueMaze struct {
	RealClues          []ScenarioCluePlan `json:"real_clues"`
	DistortedDeepClues []ScenarioCluePlan `json:"distorted_deep_clues"`
	FalseClues         []ScenarioCluePlan `json:"false_clues"`
	WitnessReports     []WitnessReport    `json:"witness_reports"`
	RedHerring         RedHerringPlan     `json:"red_herring"`
}

type ScenarioCluePlan struct {
	Name        string `json:"name"`
	Layer       string `json:"layer"`
	Location    string `json:"location"`
	Acquisition string `json:"acquisition"`
	Content     string `json:"content"`
	Use         string `json:"use"`
}

type WitnessReport struct {
	Viewpoint      string `json:"viewpoint"`
	Statement      string `json:"statement"`
	TruePart       string `json:"true_part"`
	MisleadingPart string `json:"misleading_part"`
}

type RedHerringPlan struct {
	GroupName    string   `json:"group_name"`
	SurfaceGuilt string   `json:"surface_guilt"`
	Explains     []string `json:"explains"`
	ActualAgenda string   `json:"actual_agenda"`
}

type CastPlan struct {
	AntagonistPurpose string    `json:"antagonist_purpose"`
	VictimInvolvement string    `json:"victim_involvement"`
	NPCs              []NPCPlan `json:"npcs"`
}

type NPCPlan struct {
	Name           string `json:"name"`
	PublicIdentity string `json:"public_identity"`
	Appearance     string `json:"appearance"`
	Attitude       string `json:"attitude"`
	RealMotive     string `json:"real_motive"`
	Secret         string `json:"secret"`
	DailyObsession string `json:"daily_obsession"`
	ClueFunction   string `json:"clue_function"`
	ActionLine     string `json:"action_line"`
	StatsNote      string `json:"stats_note"`
}

// ---------------------------------------------------------------------------
// Entry point: director-style multi-agent scripter harness
// ---------------------------------------------------------------------------

const defaultScripterEra = "1920s"

var gameplayCatalysts = []string{
	"社会潜入：调查员必须通过身份、关系或伪装进入受限圈层",
	"限时调查：公开事件、自然周期或社会日程制造有限调查窗口",
	"追踪狩猎：调查员沿痕迹、目击、物流或异常移动路径推进",
	"封闭空间探索：关键区域暂时封锁，出口和信息都受控",
	"多方交易：多个势力争夺同一证据、人物或物品",
	"公开事件暗线：调查在公众、媒体、警察或地方权威注视下进行",
	"路线型调查：交通线路、迁徙路线或巡回活动让线索分站展开",
	"怪物谈判：非人存在有可理解但危险的诉求，玩家可沟通或误导",
}

func randomGameplayCatalyst() string {
	return gameplayCatalysts[rand.Intn(len(gameplayCatalysts))]
}

var genScenarioMutex sync.Mutex

func RunScripterScenarioTeam(ctx context.Context, req ScenarioCreationRequest) (ScenarioCreationOutput, error) {
	genScenarioMutex.Lock()
	defer genScenarioMutex.Unlock()

	room, err := newScripterRoom(req)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	return room.Run(ctx)
}

func newScripterRoom(req ScenarioCreationRequest) (*scripterRoom, error) {
	architect, err := loadSingleAgent(models.AgentRoleArchitect)
	if err != nil {
		return nil, err
	}
	qaAgent, err := loadSingleAgent(models.AgentRoleQAGuard)
	if err != nil {
		return nil, err
	}
	parser, err := loadSingleAgent(models.AgentRoleParser)
	if err != nil {
		return nil, err
	}
	return &scripterRoom{architect: architect, qa: qaAgent, parser: parser, req: req}, nil
}

func (r *scripterRoom) prepareContext(ctx context.Context) {
	if r.req.MinPlayers <= 0 {
		r.req.MinPlayers = 1
	}
	if r.req.MaxPlayers <= 0 {
		r.req.MaxPlayers = 4
	}
	if r.req.MaxPlayers < r.req.MinPlayers {
		r.req.MaxPlayers = r.req.MinPlayers
	}
	if strings.TrimSpace(r.req.Difficulty) == "" {
		r.req.Difficulty = "normal"
	}
	if strings.TrimSpace(r.req.TargetLength) == "" {
		r.req.TargetLength = "short"
	}
	if strings.TrimSpace(r.req.Era) == "" {
		r.req.Era = defaultScripterEra
	}

	r.npcBlacklist = loadRecentNPCNameBlacklist(200)
	r.titleSamples = loadScenarioTitleSamples(80)
	if chain, err := generateGeographyChain(ctx, r.architect, r.req.Era); err != nil {
		log.Printf("[scripter] geography chain generation failed: %v", err)
	} else {
		r.geographyChain = chain
	}
}

func (r *scripterRoom) Run(ctx context.Context) (ScenarioCreationOutput, error) {
	r.prepareContext(ctx)
	if ctx.Err() != nil {
		return ScenarioCreationOutput{}, ctx.Err()
	}
	reqJSON, _ := json.Marshal(r.req)
	log.Printf("[scripter] 开始多Agent工具编排 req=%s", reqJSON)

	agents := makeVirtualAgents(r)

	background, err := generateFogBackground(ctx, r, agents.SettingScout)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("Setting Scout 失败: %w", err)
	}
	logScripterArtifact("Stage 1 Setting Scout / FogBackground", background)

	gameplay, err := generateGameplayFrame(ctx, r, agents.GameplayGrounder, background)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("Gameplay Grounder 失败: %w", err)
	}
	logScripterArtifact("Stage 1.5 Gameplay Grounder / GameplayFrame", gameplay)

	truth, err := generateTruthStack(ctx, r, agents.MysteryArchitect, background, gameplay, nil, nil)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("Mystery Architect 失败: %w", err)
	}
	logScripterArtifact("Stage 2 Mystery Architect / TruthStack", truth)

	truthReview, err := reviewTruthStack(ctx, r, agents.MisdirectionCritic, background, gameplay, truth)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("Misdirection Critic 失败: %w", err)
	}
	logScripterArtifact("Stage 2 Misdirection Critic / Review", truthReview)
	if !truthReview.Pass {
		truth, err = generateTruthStack(ctx, r, agents.MysteryArchitect, background, gameplay, &truth, &truthReview)
		if err != nil {
			return ScenarioCreationOutput{}, fmt.Errorf("TruthStack 修订失败: %w", err)
		}
		logScripterArtifact("Stage 2 Mystery Architect / Revised TruthStack", truth)
	}

	maze, err := generateClueMaze(ctx, r, agents.ClueCartographer, background, gameplay, truth, nil, nil)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("Clue Cartographer 失败: %w", err)
	}
	logScripterArtifact("Stage 3 Clue Cartographer / ClueMaze", maze)

	mazeReview, err := reviewClueMaze(ctx, r, agents.PlayerSimulator, background, gameplay, truth, maze)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("Player Simulator 失败: %w", err)
	}
	logScripterArtifact("Stage 3 Player Simulator / Review", mazeReview)
	if !mazeReview.Pass {
		maze, err = generateClueMaze(ctx, r, agents.ClueCartographer, background, gameplay, truth, &maze, &mazeReview)
		if err != nil {
			return ScenarioCreationOutput{}, fmt.Errorf("ClueMaze 修订失败: %w", err)
		}
		logScripterArtifact("Stage 3 Clue Cartographer / Revised ClueMaze", maze)
	}

	cast, err := generateCastPlan(ctx, r, agents.CastDramatist, background, gameplay, truth, maze, nil, nil)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("Cast Dramatist 失败: %w", err)
	}
	logScripterArtifact("Stage 4 Cast Dramatist / CastPlan", cast)

	castReview, err := reviewCastPlan(ctx, r, agents.ContinuityEditor, background, gameplay, truth, maze, cast)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("Continuity Editor 失败: %w", err)
	}
	logScripterArtifact("Stage 4 Continuity Editor / Review", castReview)
	if !castReview.Pass {
		cast, err = generateCastPlan(ctx, r, agents.CastDramatist, background, gameplay, truth, maze, &cast, &castReview)
		if err != nil {
			return ScenarioCreationOutput{}, fmt.Errorf("CastPlan 修订失败: %w", err)
		}
		logScripterArtifact("Stage 4 Cast Dramatist / Revised CastPlan", cast)
	}

	draft, err := assembleDraft(ctx, r, agents.ScenarioAssembler, background, gameplay, truth, maze, cast, nil, nil)
	if err != nil {
		return ScenarioCreationOutput{}, fmt.Errorf("Scenario Assembler 失败: %w", err)
	}
	log.Printf("[scripter] draft name=%q scenes=%d npcs=%d clues=%d", draft.Name, len(draft.Content.Scenes), len(draft.Content.NPCs), len(draft.Content.Clues))
	logScripterArtifact("Stage 5 Scenario Assembler / ScenarioDraft", draft)

	const maxQAAttempts = 3
	var qaResult qaGuardResult
	for attempt := 0; attempt < maxQAAttempts; attempt++ {
		if ctx.Err() != nil {
			return ScenarioCreationOutput{}, ctx.Err()
		}
		qaResult, err = runFinalQA(ctx, r, agents.FinalQAGuard, draft)
		if err != nil {
			return ScenarioCreationOutput{}, fmt.Errorf("Final QA Guard 失败: %w", err)
		}
		log.Printf("[scripter] final QA attempt=%d score=%d pass=%v must_fix=%d", attempt+1, qaResult.Score, qaResult.Pass, len(qaResult.MustFix))
		logScripterArtifact(fmt.Sprintf("Stage 6 Final QA Guard / QA attempt %d", attempt+1), qaResult)
		if qaResult.Pass && len(qaResult.MustFix) == 0 {
			return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: attempt + 1}, nil
		}
		if attempt == maxQAAttempts-1 {
			break
		}
		revised, revErr := reviseDraftByAssembler(ctx, r, agents.ScenarioAssembler, background, gameplay, truth, maze, cast, draft, qaResult.MustFix)
		if revErr != nil {
			log.Printf("[scripter] final revision failed attempt=%d: %v", attempt+1, revErr)
			break
		}
		draft = revised
		logScripterArtifact(fmt.Sprintf("Stage 5 Scenario Assembler / Revised ScenarioDraft attempt %d", attempt+1), draft)
	}

	return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: maxQAAttempts}, nil
}

func makeVirtualAgents(r *scripterRoom) scripterAgents {
	return scripterAgents{
		SettingScout: virtualScripterAgent{
			name:   "Setting Scout",
			handle: r.architect,
			system: scripterAgentSystem("Setting Scout", "只设计公开背景与调查入口，不生成幕后真相。"),
		},
		GameplayGrounder: virtualScripterAgent{
			name:   "Gameplay Grounder",
			handle: r.architect,
			system: scripterAgentSystem("Gameplay Grounder", "把玩法催化器落地到公开背景中，衍生可玩的调查结构，不生成幕后真相。"),
		},
		MysteryArchitect: virtualScripterAgent{
			name:   "Mystery Architect",
			handle: r.architect,
			system: scripterAgentSystem("Mystery Architect", "设计三层真相：假象层、表象真相、深层本相。"),
		},
		MisdirectionCritic: virtualScripterAgent{
			name:   "Misdirection Critic",
			handle: r.qa,
			system: scripterAgentSystem("Misdirection Critic", "审查误导结构和规则可行性，只返回review。"),
		},
		ClueCartographer: virtualScripterAgent{
			name:   "Clue Cartographer",
			handle: r.architect,
			system: scripterAgentSystem("Clue Cartographer", "把真相拆成稳定、有冗余路径的线索迷宫。"),
		},
		PlayerSimulator: virtualScripterAgent{
			name:   "Player Simulator",
			handle: r.qa,
			system: scripterAgentSystem("Player Simulator", "模拟玩家推进路径，审查线索迷宫是否可玩。"),
		},
		CastDramatist: virtualScripterAgent{
			name:   "Cast Dramatist",
			handle: r.architect,
			system: scripterAgentSystem("Cast Dramatist", "生成动机明确、可扮演、连接线索的NPC和关键实体。"),
		},
		ContinuityEditor: virtualScripterAgent{
			name:   "Continuity Editor",
			handle: r.qa,
			system: scripterAgentSystem("Continuity Editor", "审查NPC连续性、规则可行性和黑名单冲突。"),
		},
		ScenarioAssembler: virtualScripterAgent{
			name:   "Scenario Assembler",
			handle: r.architect,
			system: scripterAgentSystem("Scenario Assembler", "把阶段artifact编译为director-ready ScenarioDraft。"),
		},
		FinalQAGuard: virtualScripterAgent{
			name:   "Final QA Guard",
			handle: r.qa,
			system: scripterAgentSystem("Final QA Guard", "只审查最终ScenarioDraft的director可用性和规则合规。"),
		},
	}
}

func scripterAgentSystem(name, task string) string {
	return fmt.Sprintf(`<role>%s</role>
<task>%s</task>
<config nsfw="true" violent="true" sexual="true"/>
<language>zh</language>`, name, task)
}

func logScripterArtifact(stage string, artifact any) {
	bs, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		log.Printf("[scripter-artifact] %s marshal failed: %v", stage, err)
		return
	}
	log.Printf("[scripter-artifact] %s\n%s", stage, string(bs))
}

func runScripterToolStage(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, spec scripterStageSpec) (scripterToolCall, error) {
	if spec.MaxTurns <= 0 {
		spec.MaxTurns = 8
	}
	if spec.SchemaExample == "" {
		spec.SchemaExample = scripterSchemaExample(spec.ExpectedPayload)
	}

	systemPrompt := agent.system + "\n" + spec.SystemPrompt + "\n" + scripterToolProtocol(spec)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: agent.handle.systemPrompt(systemPrompt)},
		{Role: "user", Content: spec.UserPrompt},
	}

	ruleChecked := false
	for turn := 0; turn < spec.MaxTurns; turn++ {
		if ctx.Err() != nil {
			return scripterToolCall{}, ctx.Err()
		}
		log.Printf("[scripter:%s] turn=%d agent=%s", spec.Name, turn+1, agent.name)
		raw, err := agent.handle.provider.Chat(ctx, msgs)
		if err != nil {
			return scripterToolCall{}, err
		}
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls, err := parseScripterToolCalls(ctx, room.parser, raw, spec.SchemaExample)
		if err != nil {
			return scripterToolCall{}, fmt.Errorf("%s tool call JSON 解析失败: %w", spec.Name, err)
		}
		if len(calls) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "你输出了空数组。请输出至少一个有效tool call；若尚未查规则，先调用check_rule或read_rulebook_const。"})
			continue
		}

		if hasScripterRuleCalls(calls) {
			feedback := executeScripterRuleCalls(ctx, calls, spec.Name)
			if strings.TrimSpace(feedback) == "" {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "本轮包含规则查询意图，但没有有效question/constant。请用非空question调用check_rule，或用有效constant调用read_rulebook_const；本轮禁止response。"})
				continue
			}
			ruleChecked = true
			if hasScripterResponse(calls) {
				feedback += "\n\n【协议错误】查询批和response批必须分离；本轮response已被拒收。请阅读工具结果后，下一轮再单独输出response。"
			}
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "规则书/规则专家返回如下。请基于这些结果继续；如果信息足够，下一轮只输出单个response action。\n\n" + feedback})
			continue
		}

		needContinue := false
		for _, call := range calls {
			if strings.TrimSpace(call.Action) != "response" {
				continue
			}
			if spec.RequireRuleCheck && !ruleChecked {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "本阶段必须先调用至少一次有效check_rule或read_rulebook_const，才能response。请先查询本阶段规则/约束问题。"})
				needContinue = true
				break
			}
			if err := validateScripterResponsePayload(call, spec.ExpectedPayload); err != nil {
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "response payload不符合本阶段要求：" + err.Error() + "。请只输出当前阶段对应payload的response。"})
				needContinue = true
				break
			}
			log.Printf("[scripter-reason] %s: %s", spec.Name, strings.TrimSpace(call.Reason))
			return call, nil
		}
		if needContinue {
			continue
		}

		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "本轮没有有效response，也没有有效规则查询。请遵守协议：需要信息时输出check_rule/read_rulebook_const；完成时只输出单个response。"})
	}
	return scripterToolCall{}, fmt.Errorf("%s 达到最大tool turns仍未返回有效response", spec.Name)
}

func scripterToolProtocol(spec scripterStageSpec) string {
	return fmt.Sprintf(`<tool_protocol>
- 你每一轮必须只输出一个合法JSON数组；禁止Markdown、自然语言解释、代码围栏外文本。
- 允许action: think, check_rule, read_rulebook_const, response。
- check_rule格式: {"action":"check_rule","question":"一个COC7规则或阶段约束问题"}。问题只能询问通用规则、技能、时代可达性、怪物/法术/典籍/SAN/NPC属性等约束；禁止询问本剧本隐藏答案。
- read_rulebook_const格式: {"action":"read_rulebook_const","constant":"rulebook_dir|rulebook_detail_dir|aliens|books|great_old_ones_and_gods|monsters|mythos_creatures|spells|skills"}。
- 如果本轮包含check_rule或read_rulebook_const，本轮就是查询批，禁止同时提交response；等待工具结果后下一轮再response。
- 本阶段ExpectedPayload=%s。response必须是单个数组元素，并且只填对应payload字段；不要附带其他阶段payload。
- 每个response必须包含非空reason字段，用1-3句话解释为什么这样生成或审查。
- RequireRuleCheck=%v；第一次response前必须至少完成一次有效check_rule或read_rulebook_const。
</tool_protocol>
<response_schema_example>
%s
</response_schema_example>`, spec.ExpectedPayload, spec.RequireRuleCheck, spec.SchemaExample)
}

func hasScripterRuleCalls(calls []scripterToolCall) bool {
	for _, c := range calls {
		action := strings.TrimSpace(c.Action)
		if action == "check_rule" || action == "read_rulebook_const" {
			return true
		}
	}
	return false
}

func hasScripterResponse(calls []scripterToolCall) bool {
	for _, c := range calls {
		if strings.TrimSpace(c.Action) == "response" {
			return true
		}
	}
	return false
}

func validateScripterResponsePayload(call scripterToolCall, expected string) error {
	if strings.TrimSpace(call.Reason) == "" {
		return fmt.Errorf("response必须包含非空reason")
	}
	payloads := 0
	if call.Review != nil {
		payloads++
	}
	if call.Result != nil {
		payloads++
	}
	if call.Background != nil {
		payloads++
	}
	if call.Gameplay != nil {
		payloads++
	}
	if call.Truth != nil {
		payloads++
	}
	if call.Maze != nil {
		payloads++
	}
	if call.Cast != nil {
		payloads++
	}
	if call.Draft != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("response必须且只能包含一个payload, got=%d", payloads)
	}

	switch expected {
	case "background":
		if call.Background == nil {
			return fmt.Errorf("expected background")
		}
	case "gameplay":
		if call.Gameplay == nil {
			return fmt.Errorf("expected gameplay_frame")
		}
	case "truth":
		if call.Truth == nil {
			return fmt.Errorf("expected truth")
		}
	case "maze":
		if call.Maze == nil {
			return fmt.Errorf("expected maze")
		}
	case "cast":
		if call.Cast == nil {
			return fmt.Errorf("expected cast")
		}
	case "draft":
		if call.Draft == nil {
			return fmt.Errorf("expected draft")
		}
	case "review":
		if call.Review == nil {
			return fmt.Errorf("expected review")
		}
	case "qa":
		if call.Result == nil {
			return fmt.Errorf("expected result")
		}
	default:
		return fmt.Errorf("unknown expected payload %q", expected)
	}
	return nil
}

func parseScripterToolCalls(ctx context.Context, parser agentHandle, raw string, schemaExample string) ([]scripterToolCall, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(raw))
	var calls []scripterToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil {
		return calls, nil
	} else {
		parseErr := err
		if s := strings.Index(stripped, "["); s >= 0 {
			if e := strings.LastIndex(stripped, "]"); e > s {
				candidate := stripped[s : e+1]
				if err := json.Unmarshal([]byte(candidate), &calls); err == nil {
					return calls, nil
				} else {
					parseErr = err
				}
			}
		}
		if parser.provider == nil {
			return nil, fmt.Errorf("必须输出JSON数组: %w", parseErr)
		}
		fixed, repairErr := repairJSONWith(ctx, parser, stripped, parseErr, schemaExample)
		if repairErr != nil {
			return nil, repairErr
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(fixed)), &calls); err != nil {
			return nil, fmt.Errorf("修复后仍不是scripter tool-call数组: %w", err)
		}
		return calls, nil
	}
}

func executeScripterRuleCalls(ctx context.Context, calls []scripterToolCall, stageName string) string {
	var sb strings.Builder
	var lawyerHandle agentHandle
	var lawyerLoaded bool
	var lawyerErr error
	for _, c := range calls {
		switch strings.TrimSpace(c.Action) {
		case "check_rule":
			question := strings.TrimSpace(c.Question)
			if question == "" {
				continue
			}
			if !lawyerLoaded {
				lawyerHandle, lawyerErr = loadSingleAgent(models.AgentRoleLawyer)
				lawyerLoaded = true
			}
			log.Printf("[scripter:%s] check_rule=%q", stageName, question)
			if lawyerErr != nil {
				sb.WriteString(fmt.Sprintf("【check_rule:%s】\n(lawyer agent 不可用: %v)\n\n", question, lawyerErr))
				continue
			}
			results := runLawyer(ctx, lawyerHandle, question, rulebook.GlobalIndex)
			sb.WriteString(fmt.Sprintf("【check_rule:%s】\n%s\n\n", question, formatLawyerResults(results)))
		case "read_rulebook_const":
			constant := strings.TrimSpace(c.Constant)
			if constant == "" {
				continue
			}
			log.Printf("[scripter:%s] read_rulebook_const=%q", stageName, constant)
			text := rulebook.ReadConstant(constant)
			sb.WriteString(fmt.Sprintf("【read_rulebook_const:%s】\n%s\n\n", constant, text))
		}
	}
	return strings.TrimSpace(sb.String())
}

func generateFogBackground(ctx context.Context, room *scripterRoom, agent virtualScripterAgent) (FogBackground, error) {
	reqJSON, _ := json.Marshal(room.req)
	userPrompt := fmt.Sprintf(`请执行Stage 1: Setting Scout，产出FogBackground。

<request_json>%s</request_json>
<geography_chain>%s</geography_chain>
<title_samples_to_avoid>%s</title_samples_to_avoid>

要求：
- 第一轮必须先check_rule或read_rulebook_const，重点查询时代/地点/调查员身份入口、技能装备社会可达性、禁止伪科学神话解释。
- 只生成公开背景，不生成幕后真相、怪物、仪式、反转或结局。
- 用户brief最高优先级；theme/geography只补强。
- 体现“日常之美 + 不安细节”。
- 禁止小镇、渔村、孤宅、私人日记、旧报纸、默认新英格兰套路。
- 完成后输出response.background。`, string(reqJSON), strings.Join(room.geographyChain, " → "), formatScenarioTitleBlacklist(room.titleSamples))
	call, err := runScripterToolStage(ctx, room, agent, scripterStageSpec{
		Name:             "Setting Scout",
		SystemPrompt:     `<stage>反套路背景：公开调查入口、日常之美、不安细节。</stage>`,
		UserPrompt:       userPrompt,
		ExpectedPayload:  "background",
		MaxTurns:         8,
		RequireRuleCheck: true,
		SchemaExample:    scripterSchemaExample("background"),
	})
	if err != nil {
		return FogBackground{}, err
	}
	return *call.Background, nil
}

func generateGameplayFrame(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, background FogBackground) (GameplayFrame, error) {
	reqJSON, _ := json.Marshal(room.req)
	backgroundJSON, _ := json.Marshal(background)
	selectedGameplay := randomGameplayCatalyst()
	userPrompt := fmt.Sprintf(`请执行Stage 1.5: Gameplay Grounder，产出GameplayFrame。

<request_json>%s</request_json>
<background>%s</background>
<gameplay_catalyst>%s</gameplay_catalyst>

要求：
- 第一轮必须先check_rule或read_rulebook_const，重点查询该玩法在当前时代/地点/社会结构下是否需要技能、装备、交通、追逐、潜入、社交、战斗、封闭空间或时间压力规则支持。
- gameplay_catalyst只是交互结构催化器，不是硬性剧情模板；如果它与用户brief冲突，必须把它降级为局部背景压力或局部玩法，不得覆盖brief。
- 只做公开背景衍生：解释该玩法为什么在当前背景下自然成立，补充社会/空间/制度/交通/风俗压力。
- 不生成幕后真相、怪物、仪式、真凶、深层反转或结局。
- 不允许电子游戏式机制或伪科学神话解释。
- 完成后输出response.gameplay_frame。`, string(reqJSON), string(backgroundJSON), selectedGameplay)
	call, err := runScripterToolStage(ctx, room, agent, scripterStageSpec{
		Name:             "Gameplay Grounder",
		SystemPrompt:     `<stage>玩法落地：把一个玩法催化器自然落地到公开背景，衍生可玩的调查结构。</stage>`,
		UserPrompt:       userPrompt,
		ExpectedPayload:  "gameplay",
		MaxTurns:         8,
		RequireRuleCheck: true,
		SchemaExample:    scripterSchemaExample("gameplay"),
	})
	if err != nil {
		return GameplayFrame{}, err
	}
	return *call.Gameplay, nil
}

func generateTruthStack(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, background FogBackground, gameplay GameplayFrame, previous *TruthStack, review *agentReview) (TruthStack, error) {
	backgroundJSON, _ := json.Marshal(background)
	gameplayJSON, _ := json.Marshal(gameplay)
	reqJSON, _ := json.Marshal(room.req)
	revisionBlock := ""
	if previous != nil && review != nil {
		prevJSON, _ := json.Marshal(previous)
		reviewJSON, _ := json.Marshal(review)
		revisionBlock = fmt.Sprintf("\n<previous_truth>%s</previous_truth>\n<review_feedback>%s</review_feedback>\n请只按revision_brief最小修订TruthStack。", string(prevJSON), string(reviewJSON))
	}
	userPrompt := fmt.Sprintf(`请执行Stage 2: Mystery Architect，产出TruthStack。

<request_json>%s</request_json>
<background>%s</background>
<gameplay_frame>%s</gameplay_frame>%s

要求：
- 第一轮必须先check_rule/read_rulebook_const，重点查询候选神话元素/怪物/典籍/法术是否存在、三层神话因果是否与COC常识冲突、是否出现伪科学神话解释。
- 三层真相必须自洽：假象层给3条以上表面证据；表象真相必须解释全部appearance_evidence；深层本相必须不是硬拗反转。
- WhyVeteransStopHere要能误导老玩家，使其满足于表象真相。
- DeepTruthAccessCosts写明触及深层本相需要付出的代价或跳出常规思维的方式。
- 完成后输出response.truth。`, string(reqJSON), string(backgroundJSON), string(gameplayJSON), revisionBlock)
	call, err := runScripterToolStage(ctx, room, agent, scripterStageSpec{
		Name:             "Mystery Architect",
		SystemPrompt:     `<stage>三层嵌套真相：appearance → surface → deep。</stage>`,
		UserPrompt:       userPrompt,
		ExpectedPayload:  "truth",
		MaxTurns:         9,
		RequireRuleCheck: true,
		SchemaExample:    scripterSchemaExample("truth"),
	})
	if err != nil {
		return TruthStack{}, err
	}
	return *call.Truth, nil
}

func reviewTruthStack(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, background FogBackground, gameplay GameplayFrame, truth TruthStack) (agentReview, error) {
	backgroundJSON, _ := json.Marshal(background)
	gameplayJSON, _ := json.Marshal(gameplay)
	truthJSON, _ := json.Marshal(truth)
	userPrompt := fmt.Sprintf(`请执行Stage 2 Reviewer: Misdirection Critic，审查TruthStack。

<background>%s</background>
<gameplay_frame>%s</gameplay_frame>
<truth>%s</truth>

必须先check_rule/read_rulebook_const，重点查询反转涉及的神话元素是否需要核验、表象真相是否有规则层面不可能之处。
审查项：
- surface_truth是否解释全部appearance_evidence。
- deep_truth是否不是硬拗反转。
- why_veterans_stop_here是否足以误导老玩家。
只输出response.review；不重写truth。`, string(backgroundJSON), string(gameplayJSON), string(truthJSON))
	call, err := runScripterToolStage(ctx, room, agent, scripterStageSpec{
		Name:             "Misdirection Critic",
		SystemPrompt:     `<stage>Reviewer：只审查误导与规则可行性，失败时给出最小revision_brief。</stage>`,
		UserPrompt:       userPrompt,
		ExpectedPayload:  "review",
		MaxTurns:         7,
		RequireRuleCheck: true,
		SchemaExample:    scripterSchemaExample("review"),
	})
	if err != nil {
		return agentReview{}, err
	}
	return *call.Review, nil
}

func generateClueMaze(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, background FogBackground, gameplay GameplayFrame, truth TruthStack, previous *ClueMaze, review *agentReview) (ClueMaze, error) {
	backgroundJSON, _ := json.Marshal(background)
	gameplayJSON, _ := json.Marshal(gameplay)
	truthJSON, _ := json.Marshal(truth)
	revisionBlock := ""
	if previous != nil && review != nil {
		prevJSON, _ := json.Marshal(previous)
		reviewJSON, _ := json.Marshal(review)
		revisionBlock = fmt.Sprintf("\n<previous_maze>%s</previous_maze>\n<review_feedback>%s</review_feedback>\n请只按revision_brief最小修订ClueMaze。", string(prevJSON), string(reviewJSON))
	}
	userPrompt := fmt.Sprintf(`请执行Stage 3: Clue Cartographer，产出ClueMaze。

<background>%s</background>
<gameplay_frame>%s</gameplay_frame>
<truth>%s</truth>%s

要求：
- 第一轮必须先check_rule/read_rulebook_const，重点查询线索获取方式是否符合COC技能/调查流程、法术/怪物痕迹/典籍/SAN风险、玩法循环是否需要追逐/潜入/社交/封闭空间等规则支持、禁止科学仪器检测神话式伪科学解法。
- 真实线索至少有冗余路径，避免单点失败。
- 指向deep truth的线索必须失真，不要直白暴露答案。
- witness_reports必须包含victim、perpetrator、nonhuman三视角。
- red_herring团体要能合理解释表层异常，但与核心恐怖无关。
- 完成后输出response.maze。`, string(backgroundJSON), string(gameplayJSON), string(truthJSON), revisionBlock)
	call, err := runScripterToolStage(ctx, room, agent, scripterStageSpec{
		Name:             "Clue Cartographer",
		SystemPrompt:     `<stage>线索迷宫：线索失真、矛盾目击、红鲱鱼与冗余路径。</stage>`,
		UserPrompt:       userPrompt,
		ExpectedPayload:  "maze",
		MaxTurns:         9,
		RequireRuleCheck: true,
		SchemaExample:    scripterSchemaExample("maze"),
	})
	if err != nil {
		return ClueMaze{}, err
	}
	return *call.Maze, nil
}

func reviewClueMaze(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, background FogBackground, gameplay GameplayFrame, truth TruthStack, maze ClueMaze) (agentReview, error) {
	backgroundJSON, _ := json.Marshal(background)
	gameplayJSON, _ := json.Marshal(gameplay)
	truthJSON, _ := json.Marshal(truth)
	mazeJSON, _ := json.Marshal(maze)
	userPrompt := fmt.Sprintf(`请执行Stage 3 Reviewer: Player Simulator，审查ClueMaze。

<background>%s</background>
<gameplay_frame>%s</gameplay_frame>
<truth>%s</truth>
<maze>%s</maze>

必须先check_rule/read_rulebook_const，重点查询玩家通过线索推进是否需要规则机制支持、深层线索SAN/神话知识风险是否需规则注记、玩法循环是否有单点失败。
审查项：deep clues是否太直白；red herring是否能解释表层异常；是否存在单点失败；witness_reports是否含victim/perpetrator/nonhuman三视角。
只输出response.review；不重写maze。`, string(backgroundJSON), string(gameplayJSON), string(truthJSON), string(mazeJSON))
	call, err := runScripterToolStage(ctx, room, agent, scripterStageSpec{
		Name:             "Player Simulator",
		SystemPrompt:     `<stage>Reviewer：模拟调查员推进路径，指出可玩性硬问题。</stage>`,
		UserPrompt:       userPrompt,
		ExpectedPayload:  "review",
		MaxTurns:         7,
		RequireRuleCheck: true,
		SchemaExample:    scripterSchemaExample("review"),
	})
	if err != nil {
		return agentReview{}, err
	}
	return *call.Review, nil
}

func generateCastPlan(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, background FogBackground, gameplay GameplayFrame, truth TruthStack, maze ClueMaze, previous *CastPlan, review *agentReview) (CastPlan, error) {
	backgroundJSON, _ := json.Marshal(background)
	gameplayJSON, _ := json.Marshal(gameplay)
	truthJSON, _ := json.Marshal(truth)
	mazeJSON, _ := json.Marshal(maze)
	revisionBlock := ""
	if previous != nil && review != nil {
		prevJSON, _ := json.Marshal(previous)
		reviewJSON, _ := json.Marshal(review)
		revisionBlock = fmt.Sprintf("\n<previous_cast>%s</previous_cast>\n<review_feedback>%s</review_feedback>\n请只按revision_brief最小修订CastPlan。", string(prevJSON), string(reviewJSON))
	}
	userPrompt := fmt.Sprintf(`请执行Stage 4: Cast Dramatist，产出CastPlan。

<background>%s</background>
<gameplay_frame>%s</gameplay_frame>
<truth>%s</truth>
<maze>%s</maze>
<recent_npc_name_blacklist>%s</recent_npc_name_blacklist>%s

要求：
- 第一轮必须先check_rule/read_rulebook_const，重点查询人类NPC属性范围/技能常识；非人、混血、神话相关身份；法术/典籍/神话物品。
- NPC不能全知；每人必须与线索、红鲱鱼或真相有明确连接。
- 每个NPC必须有日常执念、公开身份、外貌/态度、真实动机、秘密、行动线、stats_note。
- 不复用近期NPC姓名。
- 完成后输出response.cast。`, string(backgroundJSON), string(gameplayJSON), string(truthJSON), string(mazeJSON), formatNPCNameBlacklist(room.npcBlacklist), revisionBlock)
	call, err := runScripterToolStage(ctx, room, agent, scripterStageSpec{
		Name:             "Cast Dramatist",
		SystemPrompt:     `<stage>动机与NPC：人性化动机、日常执念、线索功能、行动线。</stage>`,
		UserPrompt:       userPrompt,
		ExpectedPayload:  "cast",
		MaxTurns:         9,
		RequireRuleCheck: true,
		SchemaExample:    scripterSchemaExample("cast"),
	})
	if err != nil {
		return CastPlan{}, err
	}
	return *call.Cast, nil
}

func reviewCastPlan(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, background FogBackground, gameplay GameplayFrame, truth TruthStack, maze ClueMaze, cast CastPlan) (agentReview, error) {
	backgroundJSON, _ := json.Marshal(background)
	gameplayJSON, _ := json.Marshal(gameplay)
	truthJSON, _ := json.Marshal(truth)
	mazeJSON, _ := json.Marshal(maze)
	castJSON, _ := json.Marshal(cast)
	userPrompt := fmt.Sprintf(`请执行Stage 4 Reviewer: Continuity Editor，审查CastPlan。

<background>%s</background>
<gameplay_frame>%s</gameplay_frame>
<truth>%s</truth>
<maze>%s</maze>
<cast>%s</cast>
<recent_npc_name_blacklist>%s</recent_npc_name_blacklist>

必须先check_rule/read_rulebook_const，重点查询NPC stats/spells/mythos identity是否规则可行、NPC行动线是否需要规则机制支持，NPC能否支撑gameplay_frame中的玩家行动。
审查项：NPC是否全知；是否与线索/红鲱鱼/真相连接；是否复用黑名单姓名；是否每人有日常执念。
只输出response.review；不重写cast。`, string(backgroundJSON), string(gameplayJSON), string(truthJSON), string(mazeJSON), string(castJSON), formatNPCNameBlacklist(room.npcBlacklist))
	call, err := runScripterToolStage(ctx, room, agent, scripterStageSpec{
		Name:             "Continuity Editor",
		SystemPrompt:     `<stage>Reviewer：审查人物连续性、规则可行性和姓名黑名单。</stage>`,
		UserPrompt:       userPrompt,
		ExpectedPayload:  "review",
		MaxTurns:         7,
		RequireRuleCheck: true,
		SchemaExample:    scripterSchemaExample("review"),
	})
	if err != nil {
		return agentReview{}, err
	}
	return *call.Review, nil
}

func assembleDraft(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, background FogBackground, gameplay GameplayFrame, truth TruthStack, maze ClueMaze, cast CastPlan, previous *ScenarioDraft, mustFix []string) (ScenarioDraft, error) {
	reqJSON, _ := json.Marshal(room.req)
	backgroundJSON, _ := json.Marshal(background)
	gameplayJSON, _ := json.Marshal(gameplay)
	truthJSON, _ := json.Marshal(truth)
	mazeJSON, _ := json.Marshal(maze)
	castJSON, _ := json.Marshal(cast)
	revisionBlock := ""
	if previous != nil || len(mustFix) > 0 {
		prevJSON, _ := json.Marshal(previous)
		revisionBlock = fmt.Sprintf("\n<previous_draft>%s</previous_draft>\n<must_fix>%s</must_fix>\n请只针对must_fix做最小修订，保持content.clues已有顺序含义稳定。", string(prevJSON), strings.Join(mustFix, "\n- "))
	}
	userPrompt := fmt.Sprintf(`请执行Stage 5: Scenario Assembler，产出完整ScenarioDraft。

<request_json>%s</request_json>
<background>%s</background>
<gameplay_frame>%s</gameplay_frame>
<truth>%s</truth>
<maze>%s</maze>
<cast>%s</cast>
<clue_mapping_contract>%s</clue_mapping_contract>
<json_example>%s</json_example>
<length>%s</length>
<recent_npc_name_blacklist>%s</recent_npc_name_blacklist>
<title_samples_to_avoid>%s</title_samples_to_avoid>%s

要求：
- 第一轮必须先check_rule/read_rulebook_const，重点查询mythos elements、NPC stats、法术/典籍/怪物、SAN/HP/MP触发点、最终场景检定/危险/胜败条件。
- 只有本阶段生成最终ScenarioDraft；外部结构必须兼容models.ScenarioContent。
- content.clues必须按clue_mapping_contract顺序稳定映射，保留[真实]/[隐藏]/[误导]前缀；不要后续重排。
- content.setting和content.intro只写公开背景/入口，禁止幕后剧透。
- 每个scene.description必须包含互动对象、线索、检定、危险、出口。
- win_condition/lose_condition/partial_wins必须可由KP核查。
- 完成后输出response.draft。`, string(reqJSON), string(backgroundJSON), string(gameplayJSON), string(truthJSON), string(mazeJSON), string(castJSON), clueMappingSummary(maze), scenarioExample, lengthSpec(room.req.TargetLength), formatNPCNameBlacklist(room.npcBlacklist), formatScenarioTitleBlacklist(room.titleSamples), revisionBlock)
	call, err := runScripterToolStage(ctx, room, agent, scripterStageSpec{
		Name:             "Scenario Assembler",
		SystemPrompt:     `<stage>组装格式化：把前四阶段artifact编译成director-ready ScenarioDraft。</stage>`,
		UserPrompt:       userPrompt,
		ExpectedPayload:  "draft",
		MaxTurns:         10,
		RequireRuleCheck: true,
		SchemaExample:    scripterSchemaExample("draft"),
	})
	if err != nil {
		return ScenarioDraft{}, err
	}
	draft := *call.Draft
	applyGuardrails(&draft, room.req)
	if issues := validateDraftCompatibility(draft); len(issues) > 0 {
		log.Printf("[scripter] draft compatibility issues: %v", issues)
	}
	return draft, nil
}

func runFinalQA(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, draft ScenarioDraft) (qaGuardResult, error) {
	reqJSON, _ := json.Marshal(room.req)
	draftJSON, _ := json.Marshal(draft)
	localIssues := validateDraftCompatibility(draft)
	userPrompt := fmt.Sprintf(`请执行Stage 6: Final QA Guard，审查最终ScenarioDraft。

<request_json>%s</request_json>
<scenario_draft>%s</scenario_draft>
<local_compatibility_issues>%s</local_compatibility_issues>

必须先check_rule/read_rulebook_const，重点查询最终draft中所有神话/规则/属性/法术/怪物是否有规则支持或保守注记，以及director运行中可能触发的规则点是否明确。
QA checklist：字段完整；director可用；地图导航；线索格式和稳定下标；NPC字段；规则合规；setting/intro无剧透；胜败条件可核查。
不要重写draft，只输出response.result。pass必须满足score>=80且must_fix为空。`, string(reqJSON), string(draftJSON), strings.Join(localIssues, "\n- "))
	call, err := runScripterToolStage(ctx, room, agent, scripterStageSpec{
		Name:             "Final QA Guard",
		SystemPrompt:     `<stage>Final QA：只返回qaGuardResult，不重写draft。</stage>`,
		UserPrompt:       userPrompt,
		ExpectedPayload:  "qa",
		MaxTurns:         8,
		RequireRuleCheck: true,
		SchemaExample:    scripterSchemaExample("qa"),
	})
	if err != nil {
		return qaGuardResult{}, err
	}
	result := *call.Result
	if len(localIssues) > 0 {
		result.Pass = false
		result.MustFix = append(result.MustFix, localIssues...)
		result.Issues = append(result.Issues, localIssues...)
		if result.Score > 79 {
			result.Score = 79
		}
	}
	return result, nil
}

func reviseDraftByAssembler(ctx context.Context, room *scripterRoom, agent virtualScripterAgent, background FogBackground, gameplay GameplayFrame, truth TruthStack, maze ClueMaze, cast CastPlan, draft ScenarioDraft, mustFix []string) (ScenarioDraft, error) {
	if len(mustFix) == 0 {
		return draft, nil
	}
	return assembleDraft(ctx, room, agent, background, gameplay, truth, maze, cast, &draft, mustFix)
}

func validateDraftCompatibility(draft ScenarioDraft) []string {
	var issues []string
	if strings.TrimSpace(draft.Name) == "" {
		issues = append(issues, "ScenarioDraft.name 为空")
	}
	if strings.TrimSpace(draft.Description) == "" {
		issues = append(issues, "ScenarioDraft.description 为空")
	}
	if strings.TrimSpace(draft.Difficulty) == "" {
		issues = append(issues, "ScenarioDraft.difficulty 为空")
	}
	content := draft.Content
	if strings.TrimSpace(content.SystemPrompt) == "" {
		issues = append(issues, "content.system_prompt 为空")
	}
	if strings.TrimSpace(content.Setting) == "" {
		issues = append(issues, "content.setting 为空")
	}
	if strings.TrimSpace(content.Intro) == "" {
		issues = append(issues, "content.intro 为空")
	}
	if content.GameStartSlot < 0 || content.GameStartSlot > 47 {
		issues = append(issues, "content.game_start_slot 必须在0-47之间")
	}
	if strings.TrimSpace(content.MapDescription) == "" {
		issues = append(issues, "content.map_description 为空")
	}
	if len(content.Scenes) == 0 {
		issues = append(issues, "content.scenes 为空")
	}
	for i, scene := range content.Scenes {
		if strings.TrimSpace(scene.ID) == "" || strings.TrimSpace(scene.Name) == "" || strings.TrimSpace(scene.Description) == "" {
			issues = append(issues, fmt.Sprintf("content.scenes[%d] 缺少id/name/description", i))
		}
	}
	if len(content.NPCs) == 0 {
		issues = append(issues, "content.npcs 为空")
	}
	for i, npc := range content.NPCs {
		if strings.TrimSpace(npc.Name) == "" || strings.TrimSpace(npc.Description) == "" || strings.TrimSpace(npc.Attitude) == "" {
			issues = append(issues, fmt.Sprintf("content.npcs[%d] 缺少name/description/attitude", i))
		}
	}
	if len(content.Clues) == 0 {
		issues = append(issues, "content.clues 为空")
	}
	for i, clue := range content.Clues {
		clue = strings.TrimSpace(clue)
		if !(strings.HasPrefix(clue, "[真实]") || strings.HasPrefix(clue, "[隐藏]") || strings.HasPrefix(clue, "[误导]")) {
			issues = append(issues, fmt.Sprintf("content.clues[%d] 缺少[真实]/[隐藏]/[误导]前缀", i))
		}
	}
	if strings.TrimSpace(content.WinCondition) == "" {
		issues = append(issues, "content.win_condition 为空")
	}
	if strings.TrimSpace(content.LoseCondition) == "" {
		issues = append(issues, "content.lose_condition 为空")
	}
	return issues
}

func cluePrefixForLayer(layer string) string {
	switch strings.ToLower(strings.TrimSpace(layer)) {
	case "appearance", "surface", "real":
		return "[真实]"
	case "deep", "distorted", "distorted_deep", "hidden":
		return "[隐藏]"
	case "false", "misdirection", "red_herring":
		return "[误导]"
	default:
		return "[真实]"
	}
}

func clueMappingSummary(maze ClueMaze) string {
	var lines []string
	add := func(clues []ScenarioCluePlan) {
		for _, clue := range clues {
			prefix := cluePrefixForLayer(clue.Layer)
			lines = append(lines, fmt.Sprintf("%d. %s%s(%s): 获取=%s；内容=%s；用途=%s", len(lines), prefix, clue.Name, clue.Location, clue.Acquisition, clue.Content, clue.Use))
		}
	}
	add(maze.RealClues)
	add(maze.DistortedDeepClues)
	add(maze.FalseClues)
	if len(lines) == 0 {
		return "(ClueMaze中没有线索；Assembler必须补足并保持稳定下标)"
	}
	return strings.Join(lines, "\n")
}

func scripterSchemaExample(expected string) string {
	switch expected {
	case "background":
		return `[{"action":"response","reason":"这个公开背景保留了用户brief并把地理链转化为可调查入口。","background":{"time_and_place":"时代与地点","investigator_hook":"调查入口","daily_beauty":"日常之美","unsettling_detail":"不安细节","public_problem":"公开问题","brief_preserved":"如何保留brief","anti_trope_execution":["反套路执行"]}}]`
	case "gameplay":
		return `[{"action":"response","reason":"这个玩法催化器能从当前公开背景的制度和空间压力自然生长出来，且不覆盖幕后真相。","gameplay_frame":{"gameplay":"社会潜入：调查员必须通过身份、关系或伪装进入受限圈层","fit_reason":"为什么该玩法在此背景下成立","background_derivation":["由玩法衍生出的公开背景压力"],"investigation_loop":"玩家反复执行的调查循环","player_actions":["可执行行动"],"friction_points":["阻力来源"],"failure_pressure":"失败压力但不剧透","rule_touchpoints":["可能用到的规则点"],"boundaries":["不得覆盖brief","不得生成幕后真相"]}}]`
	case "truth":
		return `[{"action":"response","reason":"三层真相让表象证据先被合理解释，再用深层代价打开更恐怖的本相。","truth":{"appearance_belief":"最初误判","appearance_evidence":["证据1"],"surface_truth":"表象真相","surface_explains_evidence":["如何解释证据"],"why_veterans_stop_here":["误导点"],"deep_truth":"深层本相","deep_truth_access_costs":["代价"],"mythos_elements":["待核验元素"]}}]`
	case "maze":
		return `[{"action":"response","reason":"线索按真实、隐藏、误导分层，支持玩法循环并保留冗余路径。","maze":{"real_clues":[{"name":"线索名","layer":"surface","location":"地点","acquisition":"获取方式","content":"内容","use":"用途"}],"distorted_deep_clues":[{"name":"深层线索","layer":"deep","location":"地点","acquisition":"获取方式","content":"失真内容","use":"用途"}],"false_clues":[{"name":"假线索","layer":"false","location":"地点","acquisition":"获取方式","content":"内容","use":"误导用途"}],"witness_reports":[{"viewpoint":"victim","statement":"陈述","true_part":"真实部分","misleading_part":"误导部分"}],"red_herring":{"group_name":"团体","surface_guilt":"表面罪责","explains":["解释的异常"],"actual_agenda":"真实议程"}}}]`
	case "cast":
		return `[{"action":"response","reason":"NPC各自承担线索、阻力或误导功能，同时保留非主线日常执念。","cast":{"antagonist_purpose":"反派目的","victim_involvement":"受害者卷入原因","npcs":[{"name":"姓名","public_identity":"公开身份","appearance":"外貌","attitude":"态度","real_motive":"真实动机","secret":"秘密","daily_obsession":"日常执念","clue_function":"线索功能","action_line":"行动线","stats_note":"属性/规则注记"}]}}]`
	case "review":
		return `[{"action":"response","reason":"审查聚焦本阶段硬约束，问题可由一次最小修订解决。","review":{"pass":false,"issues":["问题"],"revision_brief":"最小修订指令"}}]`
	case "qa":
		return `[{"action":"response","reason":"最终QA只判断director可用性、规则合规和字段完整性，不直接改写draft。","result":{"score":75,"pass":false,"strengths":["优点"],"issues":["问题"],"must_fix":["必须修复"]}}]`
	case "draft":
		return `[{"action":"response","reason":"最终草案把所有阶段artifact编译为兼容ScenarioContent的可运行模组，并保持线索下标稳定。","draft":` + scenarioExample + `}]`
	default:
		return `[{"action":"response","reason":"解释为什么这样响应。"}]`
	}
}

// ---------------------------------------------------------------------------
// Phase 1: Generate story brief, then outline
// ---------------------------------------------------------------------------

func generateScenarioSeed(ctx context.Context, architect, parser agentHandle, req ScenarioCreationRequest, geographyChain []string, npcNameBlacklist []string) (ScenarioSeed, error) {
	reqJSON, _ := json.Marshal(req)
	userMsg := fmt.Sprintf("请生成ScenarioSeed。用户brief若非空，必须作为最高优先级创意输入，不得被地理链覆盖。\n\n<request_json>\n%s\n</request_json>\n\n<geography_chain>\n%s\n</geography_chain>\n\n<recent_npc_name_blacklist>\n%s\n</recent_npc_name_blacklist>",
		string(reqJSON), strings.Join(geographyChain, " → "), formatNPCNameBlacklist(npcNameBlacklist))
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

	userMsg := fmt.Sprintf("请按director可用性checklist审查以下ScenarioDraft。只报告字段、格式、规则、开局剧透和director运行性硬问题；不要审查剧情逻辑、人物动机、因果链、制度流程或期限是否成立。VerifiedBible仅用于确认规则注记和字段来源是否存在，不用于剧情逻辑审查。\n\n<request_json>\n%s\n</request_json>\n\n<verified_bible>\n%s\n</verified_bible>\n\n<recent_npc_name_blacklist>\n%s\n</recent_npc_name_blacklist>\n\n<scenario_draft>\n%s\n</scenario_draft>",
		string(reqJSON), string(verifiedJSON), formatNPCNameBlacklist(npcNameBlacklist), string(draftJSON))

	msgs := []llm.ChatMessage{
		{Role: "system", Content: qaAgent.systemPrompt(qaSystemPrompt)},
		{Role: "user", Content: userMsg},
	}

	const maxIter = 50
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
