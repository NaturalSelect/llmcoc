package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// ---------------------------------------------------------------------------
// 管理员上传故事直接编译（跳过 Story Architect 阶段）
// ---------------------------------------------------------------------------

// CompileStoryRequest 是管理员上传故事编译请求：管理员只提供故事全文（及可选名称），
// 系统跳过 Story Architect 生成阶段，先通过 anchor_extract 阶段自动从文档中识别神话锚点
// 与奖励概念，再让 Compiler 及后续修复/审查/规范化流程介入，即"模型只做 ETL"。
type CompileStoryRequest struct {
	StoryDocument string `json:"story_document"`
	Name          string `json:"name"`
}

// RunCompileStoryWithProgress 校验管理员上传的故事文档后，先执行 anchor_extract 阶段自动
// 识别神话锚点与奖励概念，再执行 compile→repair→logic_review→reward_agent→normalize 流水线，
// 跳过故事生成阶段。与 RunScripterScenarioTeamWithProgress 共享同一把全局锁，两条路径不会
// 并发跑生成任务。
func RunCompileStoryWithProgress(ctx context.Context, req CompileStoryRequest, progress ScripterProgressFunc) (ScenarioCreationOutput, error) {
	if strings.TrimSpace(req.StoryDocument) == "" {
		return ScenarioCreationOutput{}, fmt.Errorf("故事文档不能为空")
	}

	scripterRunMu.Lock()
	defer scripterRunMu.Unlock()

	room, err := newScripterRoom(scenarioCreationRequestFromCompileStory(req))
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	room.progressFn = progress
	scripterCounterMu.Lock()
	sessionID := fmt.Sprintf("%v", scriptSessionId-int64(scripterCounter))
	scripterCounter++
	scripterCounterMu.Unlock()
	room.sessionID = sessionID
	room.generationLog = newScripterGenerationLog(sessionID, room.req)
	ctx = context.WithValue(ctx, "session", sessionID)
	ctx = contextWithScripterGenerationLog(ctx, room.generationLog)
	room.prepareContext()

	log.Printf("[scripter] session=%s compile-story-upload start doc_len=%d",
		sessionID, len([]rune(req.StoryDocument)))

	room.emitProgress("anchor_extract", "start", "正在从文档中识别神话锚点与奖励概念…")
	extracted, err := extractAnchorFromDocument(ctx, room, req.StoryDocument)
	if err != nil {
		room.emitProgress("anchor_extract", "error", err.Error())
		return ScenarioCreationOutput{}, err
	}
	room.emitProgress("anchor_extract", "done", fmt.Sprintf("锚点提取完成：%s", truncateRunes(extracted.MythosAnchor, 40)))

	story := StoryOutput{
		Document:      req.StoryDocument,
		MythosAnchor:  extracted.MythosAnchor,
		RewardConcept: extracted.RewardConcept,
	}
	constraints := ScripterConstraints{
		Era:          room.req.Era,
		Theme:        room.req.Theme,
		TargetLength: room.req.TargetLength,
		PlayerRange:  fmt.Sprintf("%d-%d", room.req.MinPlayers, room.req.MaxPlayers),
		Difficulty:   room.req.Difficulty,
	}

	draft, iterations, err := room.compileAndFinalize(ctx, story, constraints)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	logScripterArtifact("Final ScenarioDraft", sessionID, draft)

	return ScenarioCreationOutput{
		Draft:         draft,
		IronyCore:     &IronyCore{},
		Iterations:    iterations,
		GenerationLog: room.generationLogText(),
		StoryDocument: story.Document,
	}, nil
}

// scenarioCreationRequestFromCompileStory 把管理员上传的故事编译请求映射为
// ScenarioCreationRequest，复用 newScripterRoom/normalizeScenarioCreationRequest 的既有校验与默认值逻辑；
// Theme/Era/Difficulty/MinPlayers/MaxPlayers/TargetLength 留空交给该函数填充默认值。
func scenarioCreationRequestFromCompileStory(req CompileStoryRequest) ScenarioCreationRequest {
	return ScenarioCreationRequest{
		Name: req.Name,
	}
}
