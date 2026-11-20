// NOTE: companion_tools.go 是 AI 陪玩的工具集：读取类工具返回字段化 XML，写入类工具有每次运行的配额，
// submit_action 是唯一的结束工具（solo，不能与其他工具同轮）。所有工具都经 CompanionEnv 访问房间，
// 可见性过滤由 env 负责；这里再按「网页上玩家能看到什么」约束渲染的字段，不渲染 KP 私有的会话记忆等。
// 拒绝一律写 SYSTEM REJECT: …（本次调用不产生副作用），与 Director / Compiler 工具的约定一致。
package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const (
	companionToolReadMessages  = "read_messages"
	companionToolViewCharacter = "view_character"
	companionToolViewParty     = "view_party"
	companionToolViewHandbook  = "view_handbook"
	companionToolReadOOC       = "read_ooc"
	companionToolWriteNote     = "write_note"
	companionToolPostOOC       = "post_ooc"
	companionToolSubmitAction  = "submit_action"
)

const (
	// 每次运行的写入配额：陪玩不应该刷屏。
	companionOOCQuota  = 2
	companionNoteQuota = 3

	// 单条长度上限，与网页端一致（OOC 500 字、笔记 1000 字）。
	companionOOCMaxRunes  = 500
	companionNoteMaxRunes = 1000
	// companionAnswerMaxRunes 与前端 ask.ts 的 CHAT_MAX_CHARS 和服务端 ChatStream 的上限一致。
	companionAnswerMaxRunes = 2000

	companionReadMessagesDefault = 20
	companionReadMessagesMax     = 50
	// companionReadBudget 是一次 read_messages 返回的字符预算，超出时保留最新的。
	companionReadBudget     = 20_000
	companionReadOOCDefault = 15
	companionReadOOCMax     = 30
)

// companionCleanOptions 与前端 askOptions 一致：每项去首尾空白、丢弃空串，不去重也不改顺序。
func companionCleanOptions(options []string) []string {
	var out []string
	for _, o := range options {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// companionAnswer 照抄前端 ask.ts 的 askAnswer：勾选的推荐行动按问题卡里的顺序在前，自己写的内容在后，用换行连接。
func companionAnswer(options []string, picked map[string]bool, custom string) string {
	var parts []string
	for _, o := range options {
		if picked[o] {
			parts = append(parts, o)
		}
	}
	if own := strings.TrimSpace(custom); own != "" {
		parts = append(parts, own)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// companionSession 是一次运行内的工具状态：配额用量和结束标志。只在工具循环的单个 goroutine 里使用。
type companionSession struct {
	env    CompanionEnv
	view   CompanionView
	selfID uint

	noteUsed, oocUsed int

	submitted bool // submit_action 成功
	notAsked  bool // 提交时发现点名已变化
}

func newCompanionSession(env CompanionEnv, view CompanionView) *companionSession {
	return &companionSession{env: env, view: view, selfID: view.Self.UserID}
}

// finished 报告本次运行是否已经收口：提交了回应，或点名已经变化。两种情况都不需要兜底。
func (s *companionSession) finished() bool { return s.submitted || s.notAsked }

func companionReject(format string, args ...any) toolOutcome {
	return toolOutcome{reject: "SYSTEM REJECT: " + fmt.Sprintf(format, args...)}
}

func companionBadArgs(tool string, err error) toolOutcome {
	return companionReject("%s 的参数不合法：%v。本次调用未生效，请按工具定义重新调用。", tool, err)
}

func (s *companionSession) dispatch(_ context.Context, call llm.ToolCall) toolOutcome {
	switch call.Name {
	case companionToolReadMessages:
		return s.readMessages(call.Arguments)
	case companionToolViewCharacter:
		return s.viewCharacter(call.Arguments)
	case companionToolViewParty:
		return s.viewParty(call.Arguments)
	case companionToolViewHandbook:
		return s.viewHandbook(call.Arguments)
	case companionToolReadOOC:
		return s.readOOC(call.Arguments)
	case companionToolWriteNote:
		return s.writeNote(call.Arguments)
	case companionToolPostOOC:
		return s.postOOC(call.Arguments)
	case companionToolSubmitAction:
		return s.submitAction(call.Arguments)
	}
	return companionReject("未知工具 %q，此阶段不允许调用。", call.Name)
}

// ---------------------------------------------------------------------------
// 工具定义
// ---------------------------------------------------------------------------

// companionTools 返回陪玩的全部工具；列表和描述每轮固定不变，保证提示词缓存的前缀稳定。
func companionTools() []scripterTool {
	return []scripterTool{
		{def: llm.ToolDefinition{
			Name:        companionToolReadMessages,
			Description: "翻看房间里更早的消息（KP叙事、点名、玩家行动、公开骰子），按时间从旧到新返回。before_id 为 0 或省略表示从最新往前读；想继续往前翻，把返回的 oldest_id 填进 before_id。每次最多 50 条。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"before_id": {"type": "integer", "description": "只读编号小于它的消息；0 或省略表示从最新的开始"},
					"limit": {"type": "integer", "description": "条数，默认 20，最多 50"}
				},
				"required": []
			}`),
		}},
		{def: llm.ToolDefinition{
			Name:        companionToolViewCharacter,
			Description: "查看你自己调查员的完整人物卡：属性、技能、物品、法术、疯狂与伤势、背景、关系，以及你认领的HO编号（如果有）。需要确认技能值、持有物或设定时才用。",
			Parameters:  jsonSchemaObject(`{"type": "object", "properties": {}, "required": []}`),
		}},
		{def: llm.ToolDefinition{
			Name:        companionToolViewParty,
			Description: "查看其他调查员的公开信息：姓名、职业、HP/SAN/MP、状态标记和所在位置。只含玩家在网页上看得到的内容。",
			Parameters:  jsonSchemaObject(`{"type": "object", "properties": {}, "required": []}`),
		}},
		{def: llm.ToolDefinition{
			Name:        companionToolViewHandbook,
			Description: "查看COC规则手册（JSON）。拿不准某项规则或技能用途时才用，内容较长。",
			Parameters:  jsonSchemaObject(`{"type": "object", "properties": {}, "required": []}`),
		}},
		{def: llm.ToolDefinition{
			Name:        companionToolReadOOC,
			Description: "查看桌边闲聊（OOC）：玩家之间的场外交流，不会进入KP的叙事。按时间从旧到新返回最近的若干条，最多 30 条。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"limit": {"type": "integer", "description": "条数，默认 15，最多 30"}
				},
				"required": []
			}`),
		}},
		{def: llm.ToolDefinition{
			Name:        companionToolWriteNote,
			Description: "在笔记板上记一条线索或推理。shared 默认 true，全队可见；设为 false 则只有你自己看得到。每次运行最多写 3 条，只记真正重要的内容。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"content": {"type": "string", "description": "笔记内容，最多 1000 字"},
					"shared": {"type": "boolean", "description": "是否共享给全队，默认 true"}
				},
				"required": ["content"]
			}`),
		}},
		{def: llm.ToolDefinition{
			Name:        companionToolPostOOC,
			Description: "在桌边闲聊（OOC）里说一句场外的话，所有玩家可见。每次运行最多 2 条，保持简短，不要用它来行动。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"content": {"type": "string", "description": "闲聊内容，最多 500 字"}
				},
				"required": ["content"]
			}`),
		}},
		{solo: true, def: llm.ToolDefinition{
			Name: companionToolSubmitAction,
			Description: "提交你对这次点名的回应，提交后本轮结束。必须单独一轮调用，不能与其他工具同时调用。\n" +
				"options：采纳的推荐行动，必须与问题卡里的推荐行动逐字一致（没有就不填）；text：你自己写的行动或对白，第一人称，对白放进「」，简短。\n" +
				"options 和 text 至少填一项，可以同时填；最终回应是被采纳的推荐行动在前、text 在后，总长不超过 2000 字。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"options": {"type": "array", "items": {"type": "string"}, "description": "采纳的推荐行动，逐字复制问题卡里的选项"},
					"text": {"type": "string", "description": "你自己写的行动或对白"}
				},
				"required": []
			}`),
		}},
	}
}

// ---------------------------------------------------------------------------
// 读取类工具
// ---------------------------------------------------------------------------

// companionAttrs 拼属性串，参数是 名字, 值 成对出现；值为空串的属性省略，值经 xmlAttr 转义。
func companionAttrs(kv ...string) string {
	var sb strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			continue
		}
		fmt.Fprintf(&sb, ` %s="%s"`, kv[i], xmlAttr(kv[i+1]))
	}
	return sb.String()
}

func companionClamp(n, def, hi int) int {
	if n <= 0 {
		return def
	}
	return min(n, hi)
}

func (s *companionSession) readMessages(raw string) toolOutcome {
	var args struct {
		BeforeID int `json:"before_id"`
		Limit    int `json:"limit"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return companionBadArgs(companionToolReadMessages, err)
	}
	if args.BeforeID < 0 {
		return companionReject("before_id 不能为负数。")
	}
	limit := companionClamp(args.Limit, companionReadMessagesDefault, companionReadMessagesMax)
	// NOTE: 多取一条只为判断更早处还有没有消息。
	msgs, err := s.env.Messages(0, uint(args.BeforeID), limit+1)
	if err != nil {
		return companionReject("读取消息失败：%v", err)
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[len(msgs)-limit:]
	}
	lines, oldest, omitted := companionRenderNewest(msgs, s.selfID, companionReadBudget)
	if oldest == 0 && len(msgs) > 0 {
		oldest = msgs[0].ID // 这批消息全部不可渲染时也要给出翻页锚点
	}
	hasMore = hasMore || omitted > 0
	var sb strings.Builder
	sb.WriteString("<messages")
	if args.BeforeID > 0 {
		fmt.Fprintf(&sb, ` before_id="%d"`, args.BeforeID)
	}
	fmt.Fprintf(&sb, ` count="%d" has_more="%t"`, len(lines), hasMore)
	if oldest > 0 {
		fmt.Fprintf(&sb, ` oldest_id="%d"`, oldest)
	}
	sb.WriteString(">\n")
	if len(lines) == 0 {
		sb.WriteString("（没有更多消息）\n")
	}
	for _, l := range lines {
		sb.WriteString(l + "\n")
	}
	sb.WriteString("</messages>")
	return toolOutcome{result: sb.String()}
}

func (s *companionSession) viewCharacter(raw string) toolOutcome {
	var args struct{}
	if err := decodeStrict(raw, &args); err != nil {
		return companionBadArgs(companionToolViewCharacter, err)
	}
	return toolOutcome{result: renderCompanionCharacter(s.view.Self)}
}

func (s *companionSession) viewParty(raw string) toolOutcome {
	var args struct{}
	if err := decodeStrict(raw, &args); err != nil {
		return companionBadArgs(companionToolViewParty, err)
	}
	return toolOutcome{result: renderCompanionParty(s.view.Party)}
}

func (s *companionSession) viewHandbook(raw string) toolOutcome {
	var args struct{}
	if err := decodeStrict(raw, &args); err != nil {
		return companionBadArgs(companionToolViewHandbook, err)
	}
	text, err := s.env.Handbook()
	if err != nil {
		return companionReject("读取手册失败：%v", err)
	}
	return toolOutcome{result: "<handbook format=\"json\">\n" + xmlEscape(text) + "\n</handbook>"}
}

func (s *companionSession) readOOC(raw string) toolOutcome {
	var args struct {
		Limit int `json:"limit"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return companionBadArgs(companionToolReadOOC, err)
	}
	limit := companionClamp(args.Limit, companionReadOOCDefault, companionReadOOCMax)
	rows, err := s.env.OOC(limit)
	if err != nil {
		return companionReject("读取桌边闲聊失败：%v", err)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "<ooc_messages count=\"%d\">\n", len(rows))
	for _, r := range rows {
		self := ""
		if s.selfID != 0 && r.UserID == s.selfID {
			self = "true"
		}
		fmt.Fprintf(&sb, "<ooc id=\"%d\"%s>%s</ooc>\n", r.ID,
			companionAttrs("from", r.Username, "character", r.CharacterName, "self", self), xmlEscape(strings.TrimSpace(r.Content)))
	}
	sb.WriteString("</ooc_messages>")
	return toolOutcome{result: sb.String()}
}

// renderCompanionCharacter 渲染自己的完整卡面。不含 SessionMemory：那是 KP 跨轮记住的隐藏信息，玩家看不到。
func renderCompanionCharacter(p models.SessionPlayer) string {
	card := p.CharacterCard
	st := card.Stats.Data
	var sb strings.Builder
	sb.WriteString("<character" + companionAttrs(
		"name", card.Name, "race", card.Race, "gender", card.Gender, "age", compactInt(card.Age),
		"occupation", card.Occupation, "birthplace", card.Birthplace, "residence", card.Residence,
		"location", p.Location, "armor", compactInt(p.Armor)) + ">\n")
	fmt.Fprintf(&sb, "<attributes str=\"%d\" con=\"%d\" siz=\"%d\" dex=\"%d\" app=\"%d\" int=\"%d\" pow=\"%d\" edu=\"%d\" luck=\"%d\" mov=\"%d\" build=\"%d\"%s/>\n",
		st.STR, st.CON, st.SIZ, st.DEX, st.APP, st.INT, st.POW, st.EDU, st.Luck, st.MOV, st.Build, companionAttrs("db", st.DB))
	fmt.Fprintf(&sb, "<vitals hp=\"%d/%d\" mp=\"%d/%d\" san=\"%d/%d\"/>\n", st.HP, st.MaxHP, st.MP, st.MaxMP, st.SAN, st.MaxSAN)
	if card.CthulhuMythosSkill > 0 {
		fmt.Fprintf(&sb, "<mythos skill=\"%d\"/>\n", card.CthulhuMythosSkill)
	}
	if len(card.Skills.Data) > 0 {
		names := make([]string, 0, len(card.Skills.Data))
		for name := range card.Skills.Data {
			names = append(names, name)
		}
		sort.Strings(names)
		sb.WriteString("<skills>\n")
		for _, name := range names {
			fmt.Fprintf(&sb, "<skill name=\"%s\" value=\"%d\"/>\n", xmlAttr(name), card.Skills.Data[name])
		}
		sb.WriteString("</skills>\n")
	}
	if len(card.Inventory.Data) > 0 {
		sb.WriteString("<inventory>\n")
		for _, it := range card.Inventory.Data {
			count, equipped, typ := "", "", ""
			if it.Count > 1 {
				count = fmt.Sprint(it.Count)
			}
			if it.Equipped {
				equipped = "true"
			}
			if it.Type != "" && it.Type != models.ItemTypeMisc {
				typ = string(it.Type)
			}
			fmt.Fprintf(&sb, "<item%s>%s</item>\n", companionAttrs("name", it.Name, "count", count, "type", typ, "equipped", equipped), xmlEscape(it.Desc))
		}
		sb.WriteString("</inventory>\n")
	}
	if spells := trimmedNonEmpty(card.Spells.Data); len(spells) > 0 {
		sb.WriteString("<spells>\n")
		for _, sp := range spells {
			fmt.Fprintf(&sb, "<spell>%s</spell>\n", xmlEscape(sp))
		}
		sb.WriteString("</spells>\n")
	}
	if monsters := trimmedNonEmpty(card.SeenMonsters.Data); len(monsters) > 0 {
		fmt.Fprintf(&sb, "<seen_monsters>%s</seen_monsters>\n", xmlEscape(strings.Join(monsters, "、")))
	}
	wound, unconscious := "", ""
	if card.WoundState != "" && card.WoundState != "none" {
		wound = card.WoundState
	}
	if card.IsUnconscious {
		unconscious = "true"
	}
	madness := card.MadnessState
	if madness == "none" {
		madness = ""
	}
	symptom := ""
	if madness != "" {
		symptom = card.MadnessSymptom
	}
	if wound != "" || unconscious != "" || madness != "" {
		fmt.Fprintf(&sb, "<condition%s/>\n", companionAttrs("wound", wound, "unconscious", unconscious, "madness", madness, "madness_symptom", symptom))
	}
	for _, f := range []struct{ tag, text string }{
		{"backstory", card.Backstory}, {"appearance", card.Appearance}, {"traits", card.Traits},
	} {
		if text := strings.TrimSpace(f.text); text != "" {
			fmt.Fprintf(&sb, "<%s>%s</%s>\n", f.tag, xmlEscape(text), f.tag)
		}
	}
	if rels := effectiveRelations(card.SocialRelations.Data, p.SessionRelations.Data); len(rels) > 0 {
		sb.WriteString("<relations>\n")
		for _, r := range rels {
			fmt.Fprintf(&sb, "<relation%s>%s</relation>\n", companionAttrs("name", r.Name, "relationship", r.Relationship), xmlEscape(r.Note))
		}
		sb.WriteString("</relations>\n")
	}
	if len(card.Assets.Data) > 0 {
		sb.WriteString("<assets>\n")
		for _, a := range card.Assets.Data {
			fmt.Fprintf(&sb, "<asset%s>%s</asset>\n", companionAttrs("name", a.Name, "category", a.Category), xmlEscape(a.Note))
		}
		sb.WriteString("</assets>\n")
	}
	if code := strings.TrimSpace(p.HandoutCode); code != "" {
		fmt.Fprintf(&sb, "<handout%s/>\n", companionAttrs("code", code))
	}
	sb.WriteString("</character>")
	return sb.String()
}

// renderCompanionParty 只渲染其他调查员在网页上公开可见的字段：不含技能、物品、背景、秘密背景、HO 和会话记忆。
func renderCompanionParty(party []models.SessionPlayer) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<party count=\"%d\">\n", len(party))
	for _, p := range party {
		card := p.CharacterCard
		st := card.Stats.Data
		marks := companionWoundMarks(card)
		if label := companionMadnessLabel(card.MadnessState); label != "" {
			marks = append(marks, label)
		}
		fmt.Fprintf(&sb, "<investigator%s hp=\"%d/%d\" mp=\"%d/%d\" san=\"%d/%d\"%s/>\n",
			companionAttrs("name", card.Name, "occupation", card.Occupation),
			st.HP, st.MaxHP, st.MP, st.MaxMP, st.SAN, st.MaxSAN,
			companionAttrs("status", strings.Join(marks, "、"), "location", p.Location))
	}
	sb.WriteString("</party>")
	return sb.String()
}

// ---------------------------------------------------------------------------
// 写入类工具
// ---------------------------------------------------------------------------

func (s *companionSession) writeNote(raw string) toolOutcome {
	var args struct {
		Content string `json:"content"`
		Shared  *bool  `json:"shared"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return companionBadArgs(companionToolWriteNote, err)
	}
	content := strings.TrimSpace(args.Content)
	if content == "" {
		return companionReject("笔记内容不能为空。")
	}
	if n := utf8.RuneCountInString(content); n > companionNoteMaxRunes {
		return companionReject("笔记最多 %d 字（当前 %d 字），请缩短。", companionNoteMaxRunes, n)
	}
	if s.noteUsed >= companionNoteQuota {
		return companionReject("本次最多写 %d 条笔记，配额已用完。", companionNoteQuota)
	}
	shared := args.Shared == nil || *args.Shared
	if err := s.env.WriteNote(content, shared); err != nil {
		return companionReject("写笔记失败：%v", err)
	}
	s.noteUsed++
	scope := "仅自己可见"
	if shared {
		scope = "全队可见"
	}
	return toolOutcome{result: fmt.Sprintf("已记录笔记（%s）。本次还可以写 %d 条笔记。", scope, companionNoteQuota-s.noteUsed)}
}

func (s *companionSession) postOOC(raw string) toolOutcome {
	var args struct {
		Content string `json:"content"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return companionBadArgs(companionToolPostOOC, err)
	}
	content := strings.TrimSpace(args.Content)
	if content == "" {
		return companionReject("闲聊内容不能为空。")
	}
	if n := utf8.RuneCountInString(content); n > companionOOCMaxRunes {
		return companionReject("桌边消息最多 %d 字（当前 %d 字），请缩短。", companionOOCMaxRunes, n)
	}
	if s.oocUsed >= companionOOCQuota {
		return companionReject("本次最多发 %d 条桌边消息，配额已用完。", companionOOCQuota)
	}
	if err := s.env.PostOOC(content); err != nil {
		return companionReject("发送桌边消息失败：%v", err)
	}
	s.oocUsed++
	return toolOutcome{result: fmt.Sprintf("已发送桌边消息。本次还可以发 %d 条。", companionOOCQuota-s.oocUsed)}
}

// ---------------------------------------------------------------------------
// 结束工具
// ---------------------------------------------------------------------------

func (s *companionSession) submitAction(raw string) toolOutcome {
	var args struct {
		Options []string `json:"options"`
		Text    string   `json:"text"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return companionBadArgs(companionToolSubmitAction, err)
	}
	card := s.view.Ask.Options
	picked := make(map[string]bool, len(args.Options))
	for _, o := range args.Options {
		// NOTE: 只容忍首尾空白（前端的推荐行动本来就是去过空白的），其余必须逐字一致。
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if !slices.Contains(card, o) {
			if len(card) == 0 {
				return companionReject("这次点名没有推荐行动，options 里的 %q 无效。请去掉 options，把你的行动写进 text。", o)
			}
			return companionReject("options 里的 %q 不在问题卡的推荐行动里。options 必须与推荐行动逐字一致；想自己写行动请用 text。当前推荐行动：%s。", o, strings.Join(card, "｜"))
		}
		picked[o] = true
	}
	answer := companionAnswer(card, picked, args.Text)
	if answer == "" {
		return companionReject("提交内容为空：至少采纳一个推荐行动，或在 text 里写出你的行动。")
	}
	if n := utf8.RuneCountInString(answer); n > companionAnswerMaxRunes {
		return companionReject("回应最多 %d 字（当前 %d 字），请缩短后重新提交。", companionAnswerMaxRunes, n)
	}
	err := s.env.Submit(answer)
	switch {
	case err == nil:
		s.submitted = true
		return toolOutcome{result: "已提交。", done: true}
	case errors.Is(err, ErrCompanionNotAsked):
		s.notAsked = true
		return toolOutcome{result: "点名已变化，无需回应。", done: true}
	default:
		return companionReject("提交失败：%v。可以重新调用 submit_action 再试一次。", err)
	}
}
