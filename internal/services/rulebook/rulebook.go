// Package rulebook provides loading and keyword-based searching of the COC rulebook.
package rulebook

import (
	"bufio"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const RulebookDir = `
第一章 介绍

第二章 爱手艺与克苏鲁神话

第三章 创建调查员 23

第四章 技能 40

第五章 游戏系统 71

第六章 战斗 85

第七章 追逐 109

第八章 理智 129

第九章 魔法 143

第十章 主持游戏 153

第十一章 可怖传说书籍 189

第十二章 法术 205

第十三章 外星科技及其造物 230

第十四章 怪物、野兽和异界诸神 238

第十五章 模组 312

第十六章 附录 355

译名表 392
`
const RulebookDetailDir = `
第一章 介绍
1.1 游戏概览
1.2 合作与竞争
1.3 游戏范例
1.4 游戏涵盖范围
1.5 守秘人必读
1.6 游戏用具
第二章 爱手艺与克苏鲁神话
2.1 霍华德·菲利普·洛夫克拉夫特
2.2 克苏鲁神话
第三章 创建调查员
3.1 第一步：生成属性
3.2 第二步：决定职业
3.3 第三步：决定技能并分配技能点
3.4 第四步：创造背景
3.5 第五步：决定装备
3.6 快速参考：创建调查员
3.7 创建调查员的其它选项
第四章 技能
4.1技能定义
4.2技能专攻
4.3 对立技能/难度等级
4.4 孤注一掷
4.5 组合技能检定
4.6技能列表
4.7 可选规则
第五章 游戏系统
5.1 何时掷骰
5.2技能检定
5.3 孤注一掷
5.4 复数玩家为一项技能检定掷骰？
5.5 大失败与大成功
5.6 其它检定
5.7 对抗检定：玩家对抗玩家以及近战
5.8 奖励骰与惩罚骰
5.9 组合技能检定
5.10 社交技能：难度等级
5.11 经验奖励：幕间成长
5.12 信用评级与调查员开支
5.13 熟人
5.14 训练
5.15 老化
5.16 可选规则
第六章 战斗
6.1 战斗轮
6.2 近战
6.3 战技
6.4 其它战斗情况
6.5 射击
6.6 伤害和治疗
6.7 可选规则
第七章 追逐
7.1 第一部分：建立追逐
7.2 第二部分：切入追逐
7.3 第三部分：移动
7.4 第四部分：冲突
7.5 第五部分：可选规则
第八章 理智
8.1 理智值与理智检定
8.2 疯狂
8.3 疯狂的影响
8.4 疯狂的治疗与恢复
8.5 习惯恐惧
8.6 可选规则
第九章 魔法
9.1 神话典籍
9.2 使用魔法
9.3 成为相信者
9.4 可选规则
第十章 主持游戏
10.1 新手守秘人
10.2 准备进行游戏
10.3 创建调查员
10.4 非玩家角色(NPC)
10.5 最大化利用调查员背景
10.6 掷骰与检定
10.7 掌控游戏节奏
10.8 灵感检定
10.9 非玩家角色的幸运值
10.10 发放信息
10.11 洞察检定
10.12 展示材料(Handouts)
10.13 使用规则
10.14 动作场景
10.15 展现神话的恐怖
10.16 赢家与败者
10.17 失败的理智检定
10.18 惊吓玩家
10.19 魔法
10.20 完结故事
10.21 创作模组
10.22 洛氏主题
10.23 导入
10.24 那么，最后…
第十一章 可怖传说书籍
11.1 描述神话典籍
11.2 使用神话典籍
11.3 神话典籍
11.4 神秘学书籍
第十二章 法术
12.1 法术
12.2 法术变体
12.3 法术列表
第十三章 外星科技及其造物
第十四章 怪物、野兽和神话诸神
14.1 神话生物
14.2 神话诸神
14.3 经典妖魔
14.4 野兽
14.5 可选规则
第十五章 模组
第十六章 附录
`

var Aliens = func() []string {
	text := `
# 米戈 (Mi-Go)
# 古老者
# 伊斯人
# 飞天水烟
# 星之智慧教派
# 蛇人
# 深潜者
# 夏盖妖虫
	`
	lines := strings.Split(text, "\n")
	var aliens []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			aliens = append(aliens, line)
		}
	}
	return aliens
}()

var Books = func() []string {
	text := `
# 《阿齐夫》
# 《阿撒托斯及其他》
# 《迪詹之书》
# 《伊波恩之书》
# 《刻莱诺残篇》
# 《狂僧克利萨努斯的忏悔》
# 《水中之喀特》
# 《<死灵之书>中的克苏鲁》
# 《食尸鬼教团》
# 《蠕虫之秘密》
# 《埃尔特当陶片》
# 《格哈恩残篇》
# 《黄衣之王》
# 《致夏盖的安魂弥撒》
# 《怪物及其族类》
# 《不可名状的教团》
# 《死灵之书》
# 《阿齐夫》
# 《死灵之书》
# 《萨塞克斯手稿》
# 《死灵之书》
# 《巨石的子民》
# 《纳克特抄本》
# 《波纳佩圣典》
# 《格拉基启示录》
# 《拉莱耶文本》
# 《新英格兰乐土上的奇术异事》
# 《真实的魔法》
# 《赞苏石板》
# 《宣福者美多迪乌斯》
# 《翡翠石板》
# 《金枝》
# 《易经》
# 《揭开面纱的伊西斯》
# 《所罗门之钥》
# 《女巫之锤》
# 《诺查丹玛斯的预言》
# 《西欧的异教巫术崇拜》
# 《光明篇》`
	lines := strings.Split(text, "\n")
	var books []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			books = append(books, line)
		}
	}
	return books
}()

var GreadOldOnesAndGods = func() []string {
	text := `
# 阿布霍斯
# 阿特拉克.纳克亚
# 阿撒托斯
# 芭丝特
# 昌格纳·方庚
# 克图格亚
# 伟大的克苏鲁
# 赛伊格亚
# 道罗斯
# 埃霍特
# 加塔诺托亚
# 格拉基
# 哈斯塔，不可名状者
# 伊塔库亚
# 黄衣之王，哈斯塔的化身
# 黄印
# 诺登斯
# 奈亚拉托提普
# 尼约格萨
# 兰-提格斯
# 莎布-尼古拉斯
# 修德梅尔
# 撒托古亚
# 图尔兹查
# 乌波-萨斯拉
# 伊戈罗纳克
# 伊波-兹特尔
# 伊格
# 蛇之父伊格
# 犹格-索托斯
# 佐斯-奥摩格`
	lines := strings.Split(text, "\n")
	var gods []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			gods = append(gods, line)
		}
	}
	return gods
}()

var Monsters = func() []string {
	text := `
# 幽灵
# 木乃伊
# 骷髅，人类
# 吸血鬼
# 丧尸
# 蝙蝠
# 鸟
# 熊
# 鳄鱼
# 马
# 狮子
# 老鼠
# 鲨鱼
# 蛇
# 巨型乌贼
# 黄蜂和蜜蜂群
# 狼`
	lines := strings.Split(text, "\n")
	var monsters []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			monsters = append(monsters, line)
		}
	}
	return monsters
}()

var MythosCreatures = func() []string {
	text := `
# 拜亚基
# 钻地魔虫
# 星之彩
# 蠕行者
# 达贡&海德拉 (特殊深潜者)
# 黑山羊幼崽
# 深潜者
# 混种深潜者
# 巨噬蠕虫
# 空鬼
# 古老者
# 炎之精
# 飞水螅
# 无形眷族
# 妖鬼
# 食尸鬼
# 格拉基之仆
# 诺弗刻
# 伊斯之伟大种族
# 庭达罗斯的猎犬
# 恐怖猎手
# 罗伊格尔
# 米-戈，来自犹格斯的真菌
# 夜魔
# 人面鼠
# 潜沙怪
# 蛇人
# 外神仆役
# 夏盖妖虫
# 夏塔克鸟
# 修格斯
# 修格斯主宰
# 克苏鲁的星之眷族
# 星之精
# 乔乔人`
	lines := strings.Split(text, "\n")
	var creatures []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			creatures = append(creatures, line)
		}
	}
	return creatures
}()

var Spells = func() []string {
	text := `
# 基本的“僵尸创建术”
# 快速版本的僵尸创建术
# 灰色束缚（僵尸创建术变体）
# 增强版本的僵尸创建术
# 坟墓之吻（僵尸创建术变体）
# 惊悚版本的僵尸创建术
# 活尸制造术（僵尸创建术变体）
# 灵魂分配术
# 耶德·艾塔德放逐术
# 束缚术
# 刀锋祝福术
# 戈尔戈罗斯形体扭曲术
# 深渊之息
# 黄金蜂蜜酒酿造术
# 请神术与送神术
# 请神术
# 阿撒托斯请神术
# 克图格亚请神术
# 伊塔库亚请神术
# 尼约格萨请神术
# 莎布- 尼古拉丝请神术
# 犹格-索托斯请神术
# 送神术
# 命名送神术
# 致盲术/治盲术
# 透特之咏
# 记忆模糊术
# 尼约格萨紧握术
# 相貌吞食术
# 联络术
# 钻地魔虫联络术
# 深潜者联络术
# 古老者联络术
# 飞水螅联络术
# 无形眷族联络术
# 食尸鬼联络术
# 诺弗-刻联络术
# 廷达洛斯之猎犬联络术
# 米-戈联络术
# 人面鼠联络术
# 潜沙怪联络术
# 外神仆役联络术
# 死灵联络术
# 克苏鲁的星之眷族联络术
# 伊斯人联络术
# 通神术
# 昌格纳·方庚通神术
# 克苏鲁通神术
# 艾霍特通神术
# 诺登斯通神术
# 奈亚拉托提普通神术
# 撒托古亚通神术
# 伊戈罗纳克通神术
# 纳克-提特障壁创建术
# 拉莱耶造雾术
# 僵尸创建术
# 腐烂外皮之诅咒
# 致死术
# 支配术
# 阿撒托斯的恐怖诅咒
# 苏莱曼之尘
# 旧印开光术
# 附魔法术
# 书册附魔术
# 刀具附魔术
# 笛子附魔术
# 祭刀附魔术
# 哨子附魔术
# 迷身术
# 邪眼术
# 犹格-索托斯之拳
# 血肉防护术
# 时空门法术
# 时空门 (空间门)
# 时空门搜寻术
# 时空箱创建术
# 时光门
# 时空门观察术
# 绿腐术
# 恐惧植入术
# 血肉熔解术
# 心理暗示术
# 精神震爆术
# 精神交换术
# 精神转移术
# 塔昆·阿提普之镜
# 伊本-加齐之粉
# 蒲林的埃及十字架
# 修德·梅尔之赤印
# 复活术
# 枯萎术
# 哈斯塔之歌
# 召唤法术
# 命令的形式
# 拜亚基召唤术
# 空鬼召唤术
# 炎之精召唤术
# 恐怖猎手召唤术
# 外神仆役召唤术
# 星之精召唤术
# 独立束缚术
# 拜亚基束缚术
# 黑山羊幼崽束缚术
# 空鬼束缚术
# 炎之精束缚术
# 恐怖猎手束缚术
# 夜魔束缚术
# 星之精束缚术
# 维瑞之印
# 守卫术
# 忘却之波
# 肢体调萎术
# 真言术
# 折磨术`
	lines := strings.Split(text, "\n")
	var spells []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			spells = append(spells, line)
		}
	}
	return spells
}()

// AvailableConstantKeys returns names that can be used by agent tool calls.
func AvailableConstantKeys() []string {
	return []string{
		"rulebook_dir",
		"rulebook_detail_dir",
		"aliens",
		"books",
		"great_old_ones_and_gods",
		"monsters",
		"mythos_creatures",
		"spells",
	}
}

// ReadConstant returns the requested rulebook constant in plain text.
func ReadConstant(name string) string {
	key := normalizeConstKey(name)

	switch key {
	case "rulebook_dir":
		return strings.TrimSpace(RulebookDir)
	case "rulebook_detail_dir":
		return strings.TrimSpace(RulebookDetailDir)
	case "aliens":
		return formatList("aliens", Aliens)
	case "books":
		return formatList("books", Books)
	case "great_old_ones_and_gods":
		return formatList("great_old_ones_and_gods", GreadOldOnesAndGods)
	case "monsters":
		return formatList("monsters", Monsters)
	case "mythos_creatures":
		return formatList("mythos_creatures", MythosCreatures)
	case "spells":
		return formatList("spells", Spells)
	default:
		return "unknown constant: " + name + "\navailable: " + strings.Join(AvailableConstantKeys(), ", ")
	}
}

func normalizeConstKey(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	repl := strings.NewReplacer("-", "_", " ", "_", ".", "_", "/", "_")
	key = repl.Replace(key)

	switch key {
	case "rulebookdir":
		return "rulebook_dir"
	case "rulebookdetaildir", "rulebook_detaildir":
		return "rulebook_detail_dir"
	case "greadoldonesandgods", "gread_old_ones_and_gods", "greatoldonesandgods":
		return "great_old_ones_and_gods"
	case "mythoscreatures":
		return "mythos_creatures"
	default:
		return key
	}
}

func formatList(name string, items []string) string {
	if len(items) == 0 {
		return name + "\n(total=0)"
	}
	var sb strings.Builder
	sb.WriteString(name)
	sb.WriteString("\n")
	sb.WriteString("(total=")
	sb.WriteString(strconv.Itoa(len(items)))
	sb.WriteString(")\n")
	for i := 0; i < len(items); i++ {
		sb.WriteString("- ")
		sb.WriteString(items[i])
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

// Section represents a single top-level chapter/section of the rulebook.
type Section struct {
	Title   string
	Content string
}

// Index is a slice of rulebook sections loaded from a Markdown file.
type Index []Section

// GlobalIndex holds the loaded rulebook sections, populated at startup.
var GlobalIndex Index

// Load reads a Markdown file at the given path and splits it into sections
// at any Markdown heading level (lines starting with one or more '#').
func Load(path string) (Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var sections Index
	var current *Section

	scanner := bufio.NewScanner(f)
	// Increase scanner buffer for very long lines in the rulebook.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if title, ok := parseHeading(trimmed); ok {
			// Save previous section.
			if current != nil {
				sections = append(sections, *current)
			}
			current = &Section{Title: title}
		} else if current != nil {
			current.Content += line + "\n"
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// Flush last section.
	if current != nil {
		sections = append(sections, *current)
	}

	return sections, nil
}

func parseHeading(line string) (string, bool) {
	if line == "" || line[0] != '#' {
		return "", false
	}
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i >= len(line) || line[i] != ' ' {
		return "", false
	}
	title := strings.TrimSpace(line[i:])
	if title == "" {
		return "", false
	}
	return title, true
}

// Search returns up to maxResults sections ranked by multi-strategy matching:
// exact phrase, keyword hits, title boosts, and Chinese fragment fuzzy overlap.
func Search(idx Index, query string, maxResults int) []Section {
	if maxResults <= 0 {
		maxResults = 3
	}
	qRaw := strings.TrimSpace(strings.ToLower(query))
	if qRaw == "" {
		return nil
	}
	qNorm := normalizeForMatch(qRaw)
	keywords := splitKeywords(qRaw)
	if len(keywords) == 0 {
		return nil
	}

	type scored struct {
		sec   Section
		score int
	}
	var hits []scored

	for _, sec := range idx {
		titleLower := strings.ToLower(sec.Title)
		contentLower := strings.ToLower(sec.Content)
		wholeLower := titleLower + "\n" + contentLower
		titleNorm := normalizeForMatch(titleLower)
		wholeNorm := normalizeForMatch(wholeLower)

		score := scoreSection(titleLower, contentLower, wholeLower, titleNorm, wholeNorm, qRaw, qNorm, keywords)
		if score > 0 {
			hits = append(hits, scored{sec, score})
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].score > hits[j].score
	})

	if len(hits) > maxResults {
		hits = hits[:maxResults]
	}

	result := make([]Section, 0, len(hits))
	for _, h := range hits {
		result = append(result, h.sec)
	}
	return result
}

func scoreSection(titleLower, contentLower, wholeLower, titleNorm, wholeNorm, qRaw, qNorm string, keywords []string) int {
	score := 0

	// Exact phrase has highest weight.
	if strings.Contains(wholeLower, qRaw) {
		score += 120
	}
	if qNorm != "" && strings.Contains(wholeNorm, qNorm) {
		score += 120
	}
	if strings.Contains(titleLower, qRaw) {
		score += 180
	}
	if qNorm != "" && strings.Contains(titleNorm, qNorm) {
		score += 180
	}

	for _, kw := range keywords {
		kwNorm := normalizeForMatch(kw)
		if strings.Contains(titleLower, kw) || (kwNorm != "" && strings.Contains(titleNorm, kwNorm)) {
			score += 40
		}
		if strings.Contains(contentLower, kw) || (kwNorm != "" && strings.Contains(wholeNorm, kwNorm)) {
			score += 18
		}

		// Fuzzy fallback for long Chinese phrases: overlap on 2-rune fragments.
		if kwNorm != "" && len([]rune(kwNorm)) >= 4 {
			overlap := bigramOverlap(wholeNorm, kwNorm)
			if overlap >= 0.5 {
				score += int(overlap * 20)
			}
		}
	}

	return score
}

func normalizeForMatch(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func bigramOverlap(haystack, needle string) float64 {
	hRunes := []rune(haystack)
	nRunes := []rune(needle)
	if len(nRunes) < 2 || len(hRunes) < 2 {
		return 0
	}

	total := len(nRunes) - 1
	if total <= 0 {
		return 0
	}

	hSet := make(map[string]struct{}, len(hRunes)-1)
	for i := 0; i < len(hRunes)-1; i++ {
		hSet[string(hRunes[i:i+2])] = struct{}{}
	}

	matched := 0
	for i := 0; i < len(nRunes)-1; i++ {
		if _, ok := hSet[string(nRunes[i:i+2])]; ok {
			matched++
		}
	}

	return float64(matched) / float64(total)
}

// Format converts sections to plain text suitable for an LLM prompt, with a
// total character cap to avoid blowing up the context window.
func Format(sections []Section, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 3000
	}
	var sb strings.Builder
	for _, sec := range sections {
		block := "【" + sec.Title + "】\n" + sec.Content + "\n"
		if sb.Len()+len(block) > maxChars {
			// Truncate last block to fit.
			remaining := maxChars - sb.Len()
			if remaining > 0 {
				sb.WriteString(block[:remaining])
				sb.WriteString("…")
			}
			break
		}
		sb.WriteString(block)
	}
	return strings.TrimSpace(sb.String())
}

// splitKeywords lowercases query and splits on whitespace / punctuation,
// then expands long tokens into smaller Chinese fragments for better recall.
func splitKeywords(query string) []string {
	// Replace common Chinese punctuation with spaces, then split on whitespace.
	replacer := strings.NewReplacer(
		"，", " ", "。", " ", "、", " ", "：", " ",
		"；", " ", "（", " ", "）", " ", "【", " ", "】", " ",
		"？", " ", "！", " ", ",", " ", ".", " ", ";", " ",
		":", " ", "(", " ", ")", " ", "[", " ", "]", " ",
	)
	cleaned := replacer.Replace(strings.ToLower(query))
	rawParts := strings.Fields(cleaned)
	out := make([]string, 0, len(rawParts)+8)

	seen := map[string]struct{}{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		out = append(out, v)
		seen[v] = struct{}{}
	}

	base := append([]string(nil), rawParts...)
	for _, p := range base {
		add(p)
		r := []rune(normalizeForMatch(p))
		if len(r) >= 4 {
			// Add 2~4 rune fragments from long phrases to improve recall for Chinese.
			for i := 0; i < len(r); i++ {
				for w := 2; w <= 4; w++ {
					if i+w <= len(r) {
						add(string(r[i : i+w]))
					}
				}
			}
		}
	}

	// Also add the whole query as one keyword to catch exact phrases.
	if len(base) > 1 {
		add(strings.ToLower(strings.TrimSpace(query)))
		qNorm := normalizeForMatch(query)
		if qNorm != "" {
			add(qNorm)
		}
	}
	return out
}
