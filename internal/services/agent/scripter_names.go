// scripter_names.go — 随机 NPC 姓名生成，供 Scripter 创作剧本时通过
// generate_npc_name 工具随机取名，替代原先依赖历史模组扫描的
// recent_npc_name_blacklist 机制。
//
// 姓名结果确定性地排除本次生成任务已发放过的姓名（由调用方通过
// room.usedNPCNames 传入），保证同一份剧本内 NPC 不重名：
//   - western：不自行维护姓氏数据，仅保留一份按性别区分的名字池，姓氏交给
//     gofakeit.LastName() 随机生成，兼顾性别正确性与姓氏多样性。
//   - chinese/japanese：Go 生态没有覆盖这两种文化的取名库，主路径改为现场
//     调用 LLM，以一个 gofakeit 随机英文名作创作种子生成地道姓名；LLM 不可用
//     或多次尝试后仍冲突/空结果时，回退到本文件内置的静态姓名池兜底。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"

	"github.com/brianvoe/gofakeit/v7"

	"github.com/llmcoc/server/internal/services/llm"
)

// westernFirstNames 是英美姓名的性别专属名字池；姓氏由 gofakeit.LastName()
// 随机提供，不再手工维护姓氏列表。
var westernFirstNames = map[string][]string{
	"male": {
		"Edward", "James", "Arthur", "Henry", "Charles", "William", "George", "Thomas", "Frederick", "Albert",
		"Walter", "Robert", "Samuel", "Joseph", "Edwin", "Francis", "Herbert", "Leonard", "Reginald", "Sidney",
		"Alfred", "Ernest", "Oswald", "Stanley", "Clarence", "Bertram", "Cecil", "Douglas", "Percival", "Nathaniel",
		"Gilbert", "Roland", "Vincent", "Norman", "Wallace", "Julius", "Maurice", "Lionel", "Kenneth", "Rupert",
		"Desmond", "Aubrey", "Barnaby", "Cyril", "Elliot", "Godfrey", "Hubert", "Ivor", "Jasper", "Leopold",
		"Mortimer", "Neville", "Osric", "Peregrine", "Quentin", "Rufus", "Silas", "Theodore", "Uriah", "Victor",
	},
	"female": {
		"Margaret", "Eleanor", "Dorothy", "Catherine", "Beatrice", "Charlotte", "Florence", "Alice", "Edith", "Mabel",
		"Agnes", "Constance", "Josephine", "Winifred", "Gladys", "Harriet", "Vivian", "Rosalind", "Amelia", "Beulah",
		"Cecilia", "Dinah", "Evelyn", "Frances", "Gertrude", "Henrietta", "Ida", "Jean", "Kathleen", "Lillian",
		"Myrtle", "Nora", "Opal", "Pearl", "Ruth", "Sylvia", "Theodora", "Ursula", "Vera", "Wilhelmina",
		"Adelaide", "Bernice", "Clara", "Doris", "Estelle", "Fern", "Genevieve", "Hazel", "Iris", "Jane",
		"Kate", "Lucy", "Mildred", "Nellie", "Olive", "Prudence", "Quinn", "Rosemary", "Susan", "Tessa",
	},
}

// npcNameFallbackPool 是 chinese/japanese 的静态姓名池，仅作 LLM 生成失败时的
// 兜底（LLM 是这两种文化圈姓名的主路径，见 generateLocalizedNPCName）。
var npcNameFallbackPool = map[string]map[string][]string{
	"chinese": {
		"male": {
			"陈建国", "林志远", "赵文博", "王大山", "李国强",
			"张明轩", "刘伟民", "黄海涛", "周振华", "吴子健",
			"徐立群", "孙耀庭", "马云飞", "朱天成", "胡永康",
			"郭建军", "何思远", "高志强", "罗文清", "梁国栋",
			"宋家豪", "郑文轩", "韩东升", "曹志明", "许世豪",
			"邓文昌", "冯建业", "曾令辉", "彭家瑞", "董志刚",
		},
		"female": {
			"王秀英", "刘雅琴", "张思雨", "李梦琪", "陈静怡",
			"杨丽娟", "赵晓丽", "黄雪梅", "周婉婷", "吴淑芬",
			"徐佳怡", "孙丽华", "马素芳", "朱雨桐", "胡春花",
			"郭美玲", "何秋菊", "高冬梅", "罗雅芳", "梁婉如",
			"宋玉兰", "郑丽萍", "韩雪莲", "曹敏慧", "许秀珍",
			"邓文静", "冯雅芝", "曾丽君", "彭婉清", "董雪琴",
		},
	},
	"japanese": {
		"male": {
			"佐藤健一", "鈴木一郎", "高橋大輔", "田中裕介", "渡辺誠",
			"伊藤隆", "山本浩二", "中村正人", "小林修", "加藤直樹",
			"吉田孝之", "山田拓也", "佐々木亮", "松本剛", "井上和也",
			"木村健太", "林俊介", "斎藤秀樹", "清水勇", "山口誠一",
			"阿部誠司", "森田隆志", "池田光男", "橋本聡", "山下和樹",
			"石川雄一", "中島大地", "前田健二", "藤田淳", "後藤正広",
		},
		"female": {
			"高橋美咲", "伊藤由紀", "渡辺真理", "佐藤陽子", "鈴木彩香",
			"田中恵美", "中村さくら", "小林奈々", "加藤真由美", "吉田千春",
			"山田久美子", "佐々木愛", "松本亜美", "井上美穂", "木村さゆり",
			"林優子", "斎藤紀子", "清水由美", "山口智子", "阿部香織",
			"森田真央", "池田梨花", "橋本菜々子", "山下瞳", "石川優",
			"中島里奈", "前田美和", "藤田真紀", "後藤舞", "岡田結衣",
		},
	},
}

func validNPCNameCultures() []string {
	return []string{"western", "chinese", "japanese"}
}

func validNPCNameGenders() []string {
	return []string{"male", "female"}
}

// pickWesternNames 确定性生成西式姓名：从性别专属名字池随机选名 + gofakeit
// 随机姓氏组合，排除 used 中已发放的组合。
func pickWesternNames(gender string, count int, used map[string]bool) ([]string, error) {
	firstPool, ok := westernFirstNames[gender]
	if !ok {
		return nil, fmt.Errorf("未知gender=%q，可选值：%s", gender, strings.Join(validNPCNameGenders(), "/"))
	}

	picked := make([]string, 0, count)
	seenThisCall := map[string]bool{}
	const maxAttempts = 40
	for attempt := 0; attempt < maxAttempts && len(picked) < count; attempt++ {
		name := firstPool[rand.Intn(len(firstPool))] + " " + gofakeit.LastName()
		key := strings.ToLower(name)
		if (used != nil && used[key]) || seenThisCall[key] {
			continue
		}
		seenThisCall[key] = true
		picked = append(picked, name)
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("生成western姓名失败，请重试")
	}
	return picked, nil
}

// pickFallbackNames 从 chinese/japanese 静态兜底池中随机挑选最多 count 个尚未
// 出现在 used 中的姓名。池子在该分组下可用姓名不足 count 个时，返回实际能凑齐
// 的数量；一个都凑不出时返回 error。
func pickFallbackNames(culture, gender string, count int, used map[string]bool) ([]string, error) {
	genderPool, ok := npcNameFallbackPool[culture]
	if !ok {
		return nil, fmt.Errorf("未知culture=%q，可选值：%s", culture, strings.Join(validNPCNameCultures(), "/"))
	}
	pool, ok := genderPool[gender]
	if !ok {
		return nil, fmt.Errorf("未知gender=%q，可选值：%s", gender, strings.Join(validNPCNameGenders(), "/"))
	}

	candidates := make([]string, len(pool))
	copy(candidates, pool)
	rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })

	picked := make([]string, 0, count)
	for _, name := range candidates {
		if used != nil && used[strings.ToLower(name)] {
			continue
		}
		picked = append(picked, name)
		if len(picked) >= count {
			break
		}
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("culture=%s gender=%s 姓名池已用尽，请换一种culture/gender组合，或自行拟定一个符合设定的姓名", culture, gender)
	}
	return picked, nil
}

// pickDeterministicNPCNames 是不依赖LLM的确定性取名路径：western 用 gofakeit +
// 性别名字池，chinese/japanese 用静态兜底姓名池。供 generate_npc_name 工具的
// western 分支、LLM 生成失败时的兜底、以及 normalizeOneshotDraft 的空姓名兜底
// 共用。
func pickDeterministicNPCNames(culture, gender string, count int, used map[string]bool) ([]string, error) {
	culture = strings.ToLower(strings.TrimSpace(culture))
	gender = strings.ToLower(strings.TrimSpace(gender))
	if count <= 0 {
		count = 1
	}
	if count > 5 {
		count = 5
	}

	if culture == "western" {
		return pickWesternNames(gender, count, used)
	}
	return pickFallbackNames(culture, gender, count, used)
}

// pickRandomNPCName 是 pickDeterministicNPCNames 的单个姓名版本，供代码内部
// 直接调用（如 normalizeOneshotDraft 填充空姓名兜底），不经过工具调用协议、
// 不涉及LLM。
func pickRandomNPCName(culture, gender string, used map[string]bool) (string, error) {
	names, err := pickDeterministicNPCNames(culture, gender, 1, used)
	if err != nil {
		return "", err
	}
	return names[0], nil
}

// markNPCNamesUsed 把姓名（小写归一化）标记为本次生成任务已发放，避免后续
// generate_npc_name 调用或兜底逻辑再次分配同一个姓名。
func markNPCNamesUsed(used map[string]bool, names ...string) {
	if used == nil {
		return
	}
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			used[name] = true
		}
	}
}

// cultureLabels/genderLabels 供 LLM 生成提示词使用的中文标签。
var cultureLabels = map[string]string{"chinese": "中文（汉语）", "japanese": "日文（日本）"}
var genderLabels = map[string]string{"male": "男性", "female": "女性"}

// sanitizeLLMName 清理LLM返回内容，只取第一行并去除常见包裹符号，同时做基本
// 长度合理性校验（防止模型输出解释性文字或空结果）。
func sanitizeLLMName(raw string) string {
	raw = strings.TrimSpace(raw)
	if idx := strings.IndexAny(raw, "\n\r"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.Trim(raw, " \t`\"'“”‘’，。；;：:（）()【】[]")
	if n := len([]rune(raw)); n == 0 || n > 12 {
		return ""
	}
	return raw
}

// generateLocalizedNPCName 通过 LLM 把一个随机英文姓名种子现场创作为地道的
// chinese/japanese 姓名。western 不调用本函数（走确定性的 pickWesternNames）。
func generateLocalizedNPCName(ctx context.Context, handle agentHandle, sessionID, culture, gender string) (string, error) {
	if !handle.isEnabled() {
		return "", fmt.Errorf("LLM provider unavailable")
	}
	seed := gofakeit.Name()
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是COC跑团剧本创作助手，只负责给NPC起一个地道自然的姓名。"},
		{Role: "user", Content: fmt.Sprintf(
			"请以英文姓名%q为灵感种子，创作一个符合克苏鲁跑团设定、地道自然的%s姓名（性别：%s）。只输出姓名本身，不要输出任何解释、标点、引号或其他文字。",
			seed, cultureLabels[culture], genderLabels[gender],
		)},
	}
	resp, err := handle.provider.Chat(ctx, sessionID+":npc_name_"+culture, msgs)
	if err != nil {
		return "", err
	}
	name := sanitizeLLMName(resp)
	if name == "" {
		return "", fmt.Errorf("LLM未返回可用姓名，原始输出：%q", resp)
	}
	return name, nil
}

// maxLLMNameAttempts 是单个姓名通过LLM生成时，因空结果/格式异常/与已发放姓名
// 冲突而重试的次数上限。
const maxLLMNameAttempts = 3

// generateLocalizedNPCNames 尝试生成 count 个不重复的本地化(chinese/japanese)
// 姓名；单个姓名生成失败或与已发放姓名冲突时重试，重试仍拿不到的名额从静态
// 兜底池补齐。
func generateLocalizedNPCNames(ctx context.Context, handle agentHandle, sessionID, culture, gender string, count int, used map[string]bool) ([]string, error) {
	picked := make([]string, 0, count)
	seenThisCall := map[string]bool{}
	for len(picked) < count {
		name, ok := "", false
		for attempt := 0; attempt < maxLLMNameAttempts; attempt++ {
			candidate, err := generateLocalizedNPCName(ctx, handle, sessionID, culture, gender)
			if err != nil {
				alog.Warn("npc name generation attempt failed", "tag", "generate_npc_name", "session", sessionID, "culture", culture, "gender", gender, "attempt", attempt+1, "err", err)
				continue
			}
			key := strings.ToLower(candidate)
			if (used != nil && used[key]) || seenThisCall[key] {
				continue
			}
			name, ok = candidate, true
			break
		}
		if !ok {
			break // 剩余名额转由静态兜底池补齐
		}
		seenThisCall[strings.ToLower(name)] = true
		picked = append(picked, name)
	}

	if len(picked) < count {
		merged := make(map[string]bool, len(used)+len(seenThisCall))
		for k, v := range used {
			if v {
				merged[k] = true
			}
		}
		for k := range seenThisCall {
			merged[k] = true
		}
		if fallback, err := pickFallbackNames(culture, gender, count-len(picked), merged); err == nil {
			picked = append(picked, fallback...)
		}
	}

	if len(picked) == 0 {
		return nil, fmt.Errorf("culture=%s gender=%s 姓名生成失败（LLM与兜底姓名池均不可用），请重试或自行拟定一个符合设定的姓名", culture, gender)
	}
	return picked, nil
}

// generateNPCNameArgs 是 generate_npc_name 工具调用参数。
type generateNPCNameArgs struct {
	Culture string `json:"culture"`
	Gender  string `json:"gender"`
	Count   int    `json:"count"`
}

// dispatchGenerateNPCName 是 generate_npc_name 工具的通用执行逻辑，供 Story
// Architect 与 Oneshot Architect 两个工具循环共用：解析参数、取名、登记到
// room.usedNPCNames、格式化为工具结果文案。western 走确定性姓名池，
// chinese/japanese 优先走 LLM 现场创作，失败时回退静态兜底池。
func dispatchGenerateNPCName(ctx context.Context, room *scripterRoom, call llm.ToolCall) toolOutcome {
	var args generateNPCNameArgs
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return toolOutcome{reject: "SYSTEM REJECT: generate_npc_name参数不是合法JSON，请重新调用。"}
	}
	culture := strings.ToLower(strings.TrimSpace(args.Culture))
	gender := strings.ToLower(strings.TrimSpace(args.Gender))
	count := args.Count
	if count <= 0 {
		count = 1
	}
	if count > 5 {
		count = 5
	}
	if gender != "male" && gender != "female" {
		return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 未知gender=%q，可选值：%s", args.Gender, strings.Join(validNPCNameGenders(), "/"))}
	}

	used := room.usedNPCNames
	sessionID := scripterSessionID(ctx, room)

	var names []string
	var err error
	switch culture {
	case "chinese", "japanese":
		names, err = generateLocalizedNPCNames(ctx, room.architect, sessionID, culture, gender, count, used)
	case "western":
		names, err = pickWesternNames(gender, count, used)
	default:
		return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 未知culture=%q，可选值：%s", args.Culture, strings.Join(validNPCNameCultures(), "/"))}
	}
	if err != nil {
		return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: %s", err.Error())}
	}

	markNPCNamesUsed(used, names...)
	if len(names) == 1 {
		return toolOutcome{result: fmt.Sprintf("随机姓名: %s", names[0])}
	}
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf("%d. %s", i+1, name)
	}
	return toolOutcome{result: fmt.Sprintf("随机姓名(%d个候选): %s", len(names), strings.Join(parts, " "))}
}
