package agent

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed jailbreaks/geminiHeader.md
var geminiJailbreakHeaderPrompt string

//go:embed jailbreaks/geminiTail.md
var geminiJailbreakTailPrompt string

//go:embed jailbreaks/minimaxHeader.md
var minimaxJailbreakHeaderPrompt string

//go:embed jailbreaks/minimaxTail.md
var minimaxJailbreakTailPrompt string

//go:embed jailbreaks/kimi.md
var kimiJailbreakPrompt string

//go:embed jailbreaks/glm.md
var glmJailbreakPrompt string

//go:embed jailbreaks/deepseek.md
var deepseekJailbreakPrompt string

//go:embed jailbreaks/qwen.md
var qwenJailbreakPrompt string

//go:embed jailbreaks/minimax-m3.1-flash.md
var minimaxM3_1FlashJailbreakPrompt string

//go:embed jailbreaks/gemini-3.8-flash.md
var gemini3_8FlashJailbreakPrompt string

//go:embed jailbreaks/glm-5.1.md
var glm5_1JailbreakPrompt string

//go:embed jailbreaks/glm-5.2.md
var glm5_2JailbreakPrompt string

//go:embed jailbreaks/gemini-3.5-flash.md
var gemini3_5FlashJailbreakPrompt string

//go:embed jailbreaks/gemini-3.6-flash.md
var gemini3_6FlashJailbreakPrompt string

// NOTE: gemini-3.5-flash-lite 使用 gemini-3.6-flash 的 jailbreak prompt
var gemini3_5FlashLitelJailbreakPrompt = gemini3_6FlashJailbreakPrompt

//go:embed jailbreaks/gemini-4-argon.md
var gemini4ArgonJailbreakPrompt string

//go:embed jailbreaks/gemini-4-carbon.md
var gemini4CarbonJailbreakPrompt string

//go:embed jailbreaks/gemini-4-hydrogen.md
var gemini4HydrogenJailbreakPrompt string

//go:embed jailbreaks/gemini-4-neon.md
var gemini4NeonJailbreakPrompt string

func matchLength(left, right string) int {
	matchLen := 0
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] == right[i] {
			matchLen++
		} else {
			break
		}
	}
	return matchLen
}

func withJailbreakPrompt(prompt string, model string) string {
	type pc struct {
		model  string
		header string
		tail   string
	}
	prompts := []pc{
		{
			model:  "gemini",
			header: geminiJailbreakHeaderPrompt,
			tail:   geminiJailbreakTailPrompt,
		},
		{
			model:  "minimax",
			header: minimaxJailbreakHeaderPrompt,
			tail:   minimaxJailbreakTailPrompt,
		},
		{
			model:  "minimax-m3.1-flash",
			header: minimaxM3_1FlashJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "kimi",
			header: kimiJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "glm",
			header: glmJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "deepseek",
			header: deepseekJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "qwen",
			header: qwenJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "gemini-3.8-flash",
			header: gemini3_8FlashJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "glm-5.1",
			header: glm5_1JailbreakPrompt,
			tail:   "",
		},
		{
			model:  "glm-5.2",
			header: glm5_2JailbreakPrompt,
			tail:   "",
		},
		{
			model:  "gemini-3.5-flash",
			header: gemini3_5FlashJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "gemini-3.6-flash",
			header: gemini3_6FlashJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "gemini-3.5-flash-lite",
			header: gemini3_5FlashLitelJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "gemini-4-argon",
			header: gemini4ArgonJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "gemini-4-carbon",
			header: gemini4CarbonJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "gemini-4-hydrogen",
			header: gemini4HydrogenJailbreakPrompt,
			tail:   "",
		},
		{
			model:  "gemini-4-neon",
			header: gemini4NeonJailbreakPrompt,
			tail:   "",
		},
	}
	match := &prompts[0]
	matchLen := 0
	for _, p := range prompts {
		if strings.Contains(model, p.model) {
			m := matchLength(model, p.model)
			if m > matchLen {
				match = &p
				matchLen = m
			}
		}
	}
	return fmt.Sprintf("%s\n%s\n%s", match.header, prompt, match.tail)
}
