// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

var writerSessionLocks sync.Map

const writerDefaultPrompt = `<system role="writer_agent" game="coc7" lang="zh-CN">
	<identity>
		你是官能小说风格的场景文字编辑，擅长清晰、具体、有节奏的 COC 叙事。根据导演指令续写当前场景。
	</identity>
	<output format="plain_text" no_markdown="true">
		充分发挥想象力, 直接输出玩家可见叙事文字，不加任何前言、标题、解释或格式标记。
	</output>
	<config nsfw="true" violent="true" sexualContent="true"/>
	<requirements>
		<rule>使用简体中文，官能小说风格，采用第三人称视角。</rule>
		<rule>NPC 对话用引号标注。</rule>
		<rule>禁止出现“SAN值”“HP”“技能值”“检定”等游戏术语。</rule>
		<rule>与上文保持连贯，不重复已描述的内容。</rule>
		<rule>人物发言禁止虚构，原话直接引用；无发言指令时只写场景、动作、环境和 NPC 反应。</rule>
		<rule>玩家行动边界：只能描写导演指令中明确给出的玩家动作/台词，以及工具已确认的结果；禁止补写玩家下一步、心理反应、同意/拒绝/沉默、跟随、移动、拿起/交出物品、攻击、施法、继续搜索、继续交谈或任何未授权后续动作。</rule>
		<rule>每段叙事要形成完整场景拍点：动作开始、环境/对象反馈、动作结束后的可互动状态；可互动状态必须停在玩家可选择的位置，不得替玩家跨过选择点。</rule>
		<rule>如果导演指令较短，也要基于已给出的行动、地点和上下文补足自然过渡；但不能新增未授权线索、结果、NPC台词、机械收益或玩家行为。</rule>
		<rule>场景转换只能描写导演指令明确要求的移动；若未明确要求移动，只能描述当前地点和可选出口，不能写玩家已经离开或到达新地点。</rule>
		<rule>进行详细的描写, 包括环境、人物动作、物件位置、光线、声音、对话反应等</rule>
		<rule>仔细思考每个细节, 将连贯精彩的画面呈现给玩家, 进行想象让人物的动作更生动具体</rule>
		<rule>禁止避讳色情描写和露骨描写</rule>
	</requirements>
	<style>
		<rule>日常/调查/移动/交谈场景要保持自然、具体、可见可闻：写人物动作、物件位置、光线、声音、对话反应，不要强行制造怪异感。</rule>
		<rule>恐怖感需要反差。只有当导演指令明确出现怪物、神话实体、异常现象、尸体、血腥、疯狂、袭击或强烈危险时，才提升到压迫、诡异、血腥或感官冲击的描写。</rule>
		<rule>怪物登场前的普通场景不要一直铺陈阴冷、黏腻、腐败、被注视、空气凝固、不可名状等套话；这些词会削弱真正异常出现时的冲击力。</rule>
		<rule>如果上一段很诡异，但本段导演指令只是普通行动或对话，应把语气拉回具体事件，不要惯性延续恐怖滤镜。</rule>
		<rule>怪物或异常真正出现时，先用一两个正常细节建立基线，再写异常打破基线，突出反差。</rule>
		<rule>避免无病呻吟和空泛心理描写。不要频繁写“某种不安”“难以言说”“仿佛有什么东西”等没有具体对象的句子。</rule>
		<rule>暴力、血腥、性暗示只在导演指令需要时使用；不要为了风格主动添加。</rule>
		<rule>保证信息的完整传达和逻辑连贯，避免为了追求风格和缩减文本长度而牺牲清晰度。</rule>
	</style>
</system>`

func writerLock(sessionID uint) *sync.Mutex {
	lock, _ := writerSessionLocks.LoadOrStore(sessionID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// RunWriter 独立生成白字描述,不参与KP主流程成败。
func RunWriter(ctx context.Context, gctx GameContext, direction string) (string, error) {
	lock := writerLock(gctx.Session.ID)
	lock.Lock()
	defer lock.Unlock()

	writerHandle, state, err := loadWriterState(gctx)
	if err != nil {
		return "", err
	}

	if err := appendWriter(ctx, writerHandle, state, direction, gctx); err != nil {
		return "", err
	}
	saveWriterHistory(gctx.Session.ID, state)
	return state.Buffer, nil
}

// RunWriterStream 流式生成白字描述,token会直接回调给上层SSE。
func RunWriterStream(ctx context.Context, gctx GameContext, direction string, onToken func(string)) (string, error) {
	lock := writerLock(gctx.Session.ID)
	lock.Lock()
	defer lock.Unlock()

	writerHandle, state, err := loadWriterState(gctx)
	if err != nil {
		return "", err
	}

	err = appendWriterStream(ctx, writerHandle, state, direction, gctx, onToken)
	if err == nil {
		saveWriterHistory(gctx.Session.ID, state)
	}
	return state.Buffer, err
}

func loadWriterState(gctx GameContext) (agentHandle, *WriterState, error) {
	handles, err := getCachedAgents(gctx.Session.ID)
	if err != nil {
		return agentHandle{}, nil, err
	}
	writerHandle := handles[models.AgentRoleWriter]
	if !writerHandle.isEnabled() {
		return agentHandle{}, nil, fmt.Errorf("writer agent 未配置或未启用")
	}

	state := &WriterState{}
	var session models.GameSession
	if err := models.DB.Select("id", "writer_history").First(&session, gctx.Session.ID).Error; err == nil {
		state.History = chatMsgsToLLM(session.WriterHistory.Data)
	} else {
		state.History = chatMsgsToLLM(gctx.Session.WriterHistory.Data)
	}
	return writerHandle, state, nil
}

// appendWriter 根据导演指令调用Writer,并把生成结果追加到本次白字缓冲。
func appendWriter(ctx context.Context, h agentHandle, state *WriterState, direction string, gctx GameContext) error {
	if !h.isEnabled() {
		return fmt.Errorf("writer agent 未配置或未启用")
	}
	msgs, direction := buildWriterMessages(h, state, direction, gctx)

	resp, err := h.provider.Chat(ctx, msgs)
	if err != nil {
		return err
	}

	debugf("Writer", "response len=%d preview=%s", len([]rune(resp)), resp)
	appendWriterResponse(state, direction, resp, true)
	return nil
}

func appendWriterStream(ctx context.Context, h agentHandle, state *WriterState, direction string, gctx GameContext, onToken func(string)) error {
	if !h.isEnabled() {
		return fmt.Errorf("writer agent 未配置或未启用")
	}
	msgs, direction := buildWriterMessages(h, state, direction, gctx)

	tokenCh, errCh, err := h.provider.ChatStream(ctx, msgs)
	if err != nil {
		return err
	}

	var resp strings.Builder
	for token := range tokenCh {
		resp.WriteString(token)
		if onToken != nil {
			onToken(token)
		}
	}
	streamErr := <-errCh
	text := resp.String()
	debugf("Writer", "stream response len=%d preview=%s", len([]rune(text)), text)
	appendWriterResponse(state, direction, text, streamErr == nil)
	return streamErr
}

func buildWriterMessages(h agentHandle, state *WriterState, direction string, gctx GameContext) ([]llm.ChatMessage, string) {
	if direction == "" {
		direction = "继续描述当前场景"
	}

	debugf("Writer", "direction=%s history_msgs=%d", direction, len(state.History))

	state.History = trimWriterHistoryForCache(state.History, 10000)

	sb := &strings.Builder{}
	sb.WriteString("<character>")
	for _, p := range gctx.Session.Players {
		card := p.CharacterCard
		line := fmt.Sprintf("<char><name>%s</name><app>%s</app><traits>%s</traits></char>\n", card.Name, card.Appearance, card.Traits)
		sb.WriteString(line)
	}
	sb.WriteString("</character>\n")
	sb.WriteString("<director_instruction>\n")
	sb.WriteString(direction)
	sb.WriteString("\n</director_instruction>\n")

	// 组装Writer消息:系统提示词、保留历史、本次导演指令。
	msgs := make([]llm.ChatMessage, 0, len(state.History)+2)
	msgs = append(msgs, llm.ChatMessage{
		Role:    "system",
		Content: h.systemPrompt(writerDefaultPrompt),
	})
	msgs = append(msgs, state.History...)
	msgs = append(msgs, llm.ChatMessage{
		Role:    "user",
		Content: sb.String(),
	})
	return msgs, direction
}

func appendWriterResponse(state *WriterState, direction, resp string, saveHistory bool) {
	if saveHistory {
		// 写回本次交换,供后续白字保持连续性。
		state.History = append(state.History,
			llm.ChatMessage{Role: "user", Content: "叙事指令:" + direction},
			llm.ChatMessage{Role: "assistant", Content: resp},
		)
	}
	if resp == "" {
		return
	}
	// 本次可能有多段Writer输出,段落之间保留空行。
	if state.Buffer != "" {
		state.Buffer += "\n\n"
	}
	state.Buffer += resp
}

func trimWriterHistoryForCache(history []llm.ChatMessage, maxRunes int) []llm.ChatMessage {
	if writerHistoryRuneCount(history) <= maxRunes {
		return history
	}

	targetRunes := maxRunes / 2
	keptRunes := 0
	keepFrom := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		keptRunes += len([]rune(history[i].Content))
		if keptRunes > targetRunes {
			break
		}
		keepFrom = i
	}

	// Keep user/assistant exchanges aligned when possible. Writer history is seeded
	// and appended in pairs, so preserving an even boundary avoids orphan messages.
	if keepFrom%2 == 1 {
		keepFrom++
	}
	if keepFrom >= len(history) {
		keepFrom = len(history) - 1
	}
	if keepFrom < 0 {
		keepFrom = 0
	}
	return history[keepFrom:]
}

func writerHistoryRuneCount(history []llm.ChatMessage) int {
	runeCount := 0
	for _, msg := range history {
		runeCount += len([]rune(msg.Content))
	}
	return runeCount
}

const characterEvolutionPrompt = `你是无限流故事的角色成长编辑。根据角色原有的背景故事、性格特征,以及本次冒险的叙事经历,更新角色的背景故事,体现冒险对角色的影响和成长。

要求:
- 保留角色的核心身份,但反映冒险带来的变化
- 背景故事可以追加新的经历
- 篇幅与原有内容相近,不要过度冗长,一般仅追加一两句话即可,如果过长请考虑总结
- 总篇幅在200字以内
- 仅输出JSON,不要任何额外文字:
{"new_backstory": "更新后的背景故事(200字以内)"}
`

// CharacterEvolutionResult is the writer agent output for a single character's evolution.
type CharacterEvolutionResult struct {
	NewBackstory string `json:"new_backstory"`
}

var evolutionExample = func() string {
	data, err := json.Marshal(CharacterEvolutionResult{})
	if err != nil {
		return ""
	}
	return string(data)
}()

// RunCharacterEvolution uses the Writer agent to generate an updated backstory and traits
// for the given character card, based on the session's WriterHistory.
// The full WriterHistory is reused as conversation context (all messages are already cached
// by the provider from the game session). Only the final evolution request is a new message.
// Returns an error if the Writer agent is not configured or the LLM call fails.
func RunCharacterEvolution(ctx context.Context, card *models.CharacterCard, writerHistory []models.ChatMsg) (CharacterEvolutionResult, error) {
	if len(writerHistory) == 0 {
		return CharacterEvolutionResult{NewBackstory: card.Backstory}, nil
	}

	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		return CharacterEvolutionResult{}, fmt.Errorf("evaluator agent 未配置: %w", err)
	}

	// Copy WriterHistory as-is — all messages hit the provider's prompt cache.
	msgs := make([]llm.ChatMessage, 0, len(writerHistory)+2)
	msgs = append(msgs, llm.ChatMessage{
		Role:    "system",
		Content: handle.systemPrompt(characterEvolutionPrompt),
	})
	for _, m := range writerHistory {
		msgs = append(msgs, llm.ChatMessage{Role: m.Role, Content: m.Content})
	}
	// Append the evolution request as the only new (non-cached) message.
	msgs = append(msgs, llm.ChatMessage{
		Role: "user",
		Content: fmt.Sprintf(
			"根据以上叙事,更新角色【%s】的背景故事(100字)。\n原背景故事:%s\n\n仅输出JSON:{\"new_backstory\": \"...\"}",
			card.Name, card.Backstory,
		),
	})

	resp, err := handle.provider.Chat(ctx, msgs)
	if err != nil {
		return CharacterEvolutionResult{}, fmt.Errorf("character evolution LLM error: %w", err)
	}

	var result CharacterEvolutionResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		for i := 0; i < 30; i++ {
			resp, err = RepairJSON(ctx, resp, err, evolutionExample)
			if err == nil {
				err = json.Unmarshal([]byte(resp), &result)
				if err == nil {
					break
				}
			}
			log.Printf("[agent] character evolution JSON parse error for %q: %v; attempt %d to repair with parser", card.Name, err, i+1)
		}
		if err != nil {
			log.Printf("[agent] character evolution JSON parse error for %q: %v", card.Name, err)
			return CharacterEvolutionResult{}, fmt.Errorf("character evolution JSON parse error: %w", err)
		}
	}

	return result, nil
}
