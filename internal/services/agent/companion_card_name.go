// companion_card_name.go — AI 陪玩卡池批量生成时给人物卡取名：沿用 generate_npc_name 的取名路径
// （随机选 western/chinese/japanese，Writer 不可用时退回静态姓名池），再把外文姓名译成中文。
package agent

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"unicode"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const (
	companionNameSession = "nosession:companion_card_name"
	// 单个姓名因取名失败、译名不合格或与已有姓名冲突而重来的次数上限，用尽后改取静态中文姓名池。
	maxCompanionNameAttempts = 5
)

var companionNameGenders = map[string]string{
	models.GenderMale:   "male",
	models.GenderFemale: "female",
}

// PickCompanionCardName 为 AI 陪玩的人物卡取一个中文姓名；gender 取 models.GenderMale / GenderFemale。
// used 里的姓名（小写归一化）不会再发放，取到的姓名由调用方登记。
func PickCompanionCardName(ctx context.Context, gender string, used map[string]bool) (string, error) {
	// NOTE: Chore 和 Writer 都不可用时 handle 是零值，取名自动退回静态姓名池，不算错误。
	handle, _ := loadChoreAgent()
	return pickCompanionCardName(ctx, handle, gender, used)
}

func pickCompanionCardName(ctx context.Context, handle agentHandle, gender string, used map[string]bool) (string, error) {
	npcGender, ok := companionNameGenders[gender]
	if !ok {
		return "", fmt.Errorf("无效的性别 %q", gender)
	}
	cultures := validNPCNameCultures()
	for attempt := 0; attempt < maxCompanionNameAttempts; attempt++ {
		culture := cultures[rand.IntN(len(cultures))]
		names, err := pickNPCNames(ctx, handle, companionNameSession, generateNPCNameArgs{Culture: culture, Gender: npcGender, Count: 1}, used)
		if err != nil {
			alog.Warn("companion card name pick failed", "culture", culture, "attempt", attempt+1, "err", err)
			continue
		}
		name, err := toChineseName(ctx, handle, names[0])
		if err != nil {
			alog.Warn("companion card name to chinese failed", "name", names[0], "attempt", attempt+1, "err", err)
			continue
		}
		if used[strings.ToLower(name)] {
			continue
		}
		return name, nil
	}
	return pickRandomNPCName("chinese", npcGender, used)
}

// isChineseName 要求姓名全部由汉字（含间隔号）组成，至少两个字；不含字母和假名。
func isChineseName(name string) bool {
	runes := []rune(name)
	if len(runes) < 2 {
		return false
	}
	for _, r := range runes {
		if r != '·' && !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}

// toChineseName 把外文姓名（英文、日文假名）译成中文；本来就是汉字的姓名原样返回，不调用 LLM。
func toChineseName(ctx context.Context, handle agentHandle, name string) (string, error) {
	if isChineseName(name) {
		return name, nil
	}
	if !handle.isEnabled() {
		return "", fmt.Errorf("LLM provider unavailable")
	}
	msgs := []llm.ChatMessage{
		{Role: "system", Content: handle.systemPrompt("你是COC跑团剧本创作助手，只负责把外文人名译成中文。")},
		{Role: "user", Content: fmt.Sprintf(
			"请把人名%q译成中文姓名：西方人名用通行的中文音译，名与姓之间用“·”分隔；日本人名用汉字写法，原文是假名的选取贴切的汉字。必须全部使用汉字，不得保留字母或假名。只输出译名本身，不要输出任何解释、标点、引号或其他文字。",
			name,
		)},
	}
	resp, err := handle.provider.Chat(ctx, companionNameSession, msgs)
	if err != nil {
		return "", err
	}
	translated := sanitizeLLMName(resp)
	if !isChineseName(translated) {
		return "", fmt.Errorf("译名不是纯中文：%q", resp)
	}
	return translated, nil
}
