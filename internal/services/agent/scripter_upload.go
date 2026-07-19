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

// CompileStoryRequest 是管理员上传故事编译请求：管理员自行提供故事全文与神话锚点，
// 系统跳过 Story Architect 生成阶段，只让 Compiler 及后续修复/审查/规范化流程介入，
// 即"模型只做 ETL"。
type CompileStoryRequest struct {
	StoryDocument string `json:"story_document"`
	MythosAnchor  string `json:"mythos_anchor"`
	RewardConcept string `json:"reward_concept"`
	Name          string `json:"name"`
	Theme         string `json:"theme"`
	Era           string `json:"era"`
	Difficulty    string `json:"difficulty"`
	MinPlayers    int    `json:"min_players"`
	MaxPlayers    int    `json:"max_players"`
	TargetLength  string `json:"target_length"`
}

// RunCompileStoryWithProgress 校验管理员上传的故事文档后，直接执行
// compile→repair→logic_review→reward_agent→normalize 流水线，跳过故事生成阶段。
// 与 RunScripterScenarioTeamWithProgress 共享同一把全局锁，两条路径不会并发跑生成任务。
func RunCompileStoryWithProgress(ctx context.Context, req CompileStoryRequest, progress ScripterProgressFunc) (ScenarioCreationOutput, error) {
	if strings.TrimSpace(req.StoryDocument) == "" {
		return ScenarioCreationOutput{}, fmt.Errorf("故事文档不能为空")
	}
	if strings.TrimSpace(req.MythosAnchor) == "" {
		return ScenarioCreationOutput{}, fmt.Errorf("神话锚点不能为空")
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

	log.Printf("[scripter] session=%s compile-story-upload start anchor=%q doc_len=%d",
		sessionID, truncateRunes(req.MythosAnchor, 80), len([]rune(req.StoryDocument)))

	story := StoryOutput{
		Document:      req.StoryDocument,
		MythosAnchor:  req.MythosAnchor,
		RewardConcept: req.RewardConcept,
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
	}, nil
}

// scenarioCreationRequestFromCompileStory 把管理员上传的故事编译请求映射为
// ScenarioCreationRequest，复用 newScripterRoom/normalizeScenarioCreationRequest 的既有校验与默认值逻辑。
func scenarioCreationRequestFromCompileStory(req CompileStoryRequest) ScenarioCreationRequest {
	return ScenarioCreationRequest{
		Name:         req.Name,
		Theme:        req.Theme,
		Era:          req.Era,
		Difficulty:   req.Difficulty,
		MinPlayers:   req.MinPlayers,
		MaxPlayers:   req.MaxPlayers,
		TargetLength: req.TargetLength,
	}
}
