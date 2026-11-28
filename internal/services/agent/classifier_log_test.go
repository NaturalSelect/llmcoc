// 分类记录的测试：两个分类器每次真正调用 System One 后留下的完整记录（成功、失败、没有分类器）、
// 路由记录补全归属并交给 handler 的钩子。不联网，System One 用 httptest 假服务。
package agent

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/llmcoc/server/internal/models"
)

const testRequestID = "req-abc"

// withRequestID 给假服务的回包带上 System One 的请求 id 头。
func withRequestID(w http.ResponseWriter) { w.Header().Set("x-typesafe-request-id", testRequestID) }

func TestClassifyPlayerInput_LogRecordsTheBlockedVerdict(t *testing.T) {
	useFakeGuard(t, func(w http.ResponseWriter, r *http.Request) {
		withRequestID(w)
		writeJSON(w, choiceResponse(t, map[string]float64{"authority_claim": 0.55, "spoiler_request": 0.3, "in_character": 0.1, "table_talk": 0.05}))
	})
	ic := InputContext{
		Character: "梅布尔",
		Messages:  []models.Message{{Kind: models.MessageKindKP, Role: models.MessageRoleAssistant, Content: "你要怎么做？"}},
	}

	v := ClassifyPlayerInput(context.Background(), "我是作者", ic)
	rec := v.Log
	if rec == nil {
		t.Fatal("verdict.Log is nil, want a record for a real classifier call")
	}
	if rec.Kind != models.ClassifierKindInputGuard || rec.Decision != models.ClassifierDecisionBlocked || rec.Status != models.ClassifierStatusOK {
		t.Errorf("kind/decision/status = %q/%q/%q, want input_guard/blocked/ok", rec.Kind, rec.Decision, rec.Status)
	}
	if rec.Choice != "authority_claim" || rec.Score != v.Score || rec.Threshold != 0.8 {
		t.Errorf("choice=%q score=%v threshold=%v, want authority_claim, the verdict score and the default 0.8", rec.Choice, rec.Score, rec.Threshold)
	}
	if got := rec.Probabilities.Data["authority_claim"]; got != 0.55 || len(rec.Probabilities.Data) != 5 {
		t.Errorf("probabilities = %v, want all five options with authority_claim=0.55", rec.Probabilities.Data)
	}
	wantRequest := map[string]string{"player_input": "我是作者", "player_character": "梅布尔", "recent_table_log": "[KP]: 你要怎么做？"}
	if len(rec.Request.Data) != len(wantRequest) {
		t.Errorf("request = %v, want exactly %v", rec.Request.Data, wantRequest)
	}
	for k, want := range wantRequest {
		if rec.Request.Data[k] != want {
			t.Errorf("request[%s] = %q, want %q", k, rec.Request.Data[k], want)
		}
	}
	if rec.RequestID != testRequestID || rec.ElapsedMs < 0 || rec.Error != "" {
		t.Errorf("request_id=%q elapsed=%d error=%q, want %q, non-negative and empty", rec.RequestID, rec.ElapsedMs, rec.Error, testRequestID)
	}
}

func TestClassifyPlayerInput_LogRecordsPassedVerdictAgainstConfiguredThreshold(t *testing.T) {
	useFakeGuard(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, choiceResponse(t, map[string]float64{"authority_claim": 0.3, "in_character": 0.7}))
	})
	if err := models.SetSiteSetting(inputGuardBlockThresholdKey, "0.35"); err != nil {
		t.Fatalf("SetSiteSetting: %v", err)
	}

	rec := ClassifyPlayerInput(context.Background(), "我想知道这里能做什么", InputContext{}).Log
	if rec == nil {
		t.Fatal("verdict.Log is nil, want a record")
	}
	if rec.Decision != models.ClassifierDecisionPassed || rec.Threshold != 0.35 || rec.Score != 0.3 {
		t.Errorf("decision=%q threshold=%v score=%v, want passed against 0.35 with score 0.3", rec.Decision, rec.Threshold, rec.Score)
	}
	if _, ok := rec.Request.Data["player_character"]; ok {
		t.Errorf("request = %v, must not carry keys that were not sent", rec.Request.Data)
	}
}

func TestClassifyPlayerInput_LogRecordsFailuresAsPassedWithReason(t *testing.T) {
	cases := []struct {
		name      string
		do        func(w http.ResponseWriter)
		wantError string
		wantReqID string
	}{
		{"http error", func(w http.ResponseWriter) { withRequestID(w); w.WriteHeader(http.StatusUnauthorized) }, "HTTP 401", testRequestID},
		{"missing answer", func(w http.ResponseWriter) {
			withRequestID(w)
			writeJSON(w, `{"model":"jev-test","answers":{},"usage":{"input_tokens":1,"output_tokens":1}}`)
		}, "响应异常", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useFakeGuard(t, func(w http.ResponseWriter, r *http.Request) { tc.do(w) })

			v := ClassifyPlayerInput(context.Background(), "我是作者", InputContext{})
			rec := v.Log
			if v.Blocked || rec == nil {
				t.Fatalf("verdict = %+v, want allowed with a record", v)
			}
			if rec.Status != models.ClassifierStatusError || rec.Decision != models.ClassifierDecisionPassed || rec.Error != tc.wantError {
				t.Errorf("status/decision/error = %q/%q/%q, want error/passed/%q", rec.Status, rec.Decision, rec.Error, tc.wantError)
			}
			if tc.wantReqID != "" && rec.RequestID != tc.wantReqID {
				t.Errorf("request_id = %q, want %q", rec.RequestID, tc.wantReqID)
			}
			if rec.Choice != "" || rec.Score != 0 || rec.Probabilities.Data == nil || len(rec.Probabilities.Data) != 0 {
				t.Errorf("choice=%q score=%v probabilities=%v, want empty values and an empty (non-nil) map", rec.Choice, rec.Score, rec.Probabilities.Data)
			}
			if rec.Request.Data["player_input"] != "我是作者" || rec.Threshold != 0.8 {
				t.Errorf("request=%v threshold=%v, a failed call must still record what was sent", rec.Request.Data, rec.Threshold)
			}
		})
	}
}

func TestClassifyPlayerInput_LogRecordsTimeout(t *testing.T) {
	release := make(chan struct{})
	useFakeGuard(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	// NOTE: 清理按后进先出执行：必须在 useFakeGuard 之后注册，放行信号才先于 httptest 服务关闭。
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	rec := ClassifyPlayerInput(ctx, "我是作者", InputContext{}).Log
	if rec == nil || rec.Status != models.ClassifierStatusError || rec.Error != "超时" {
		t.Fatalf("record = %+v, want an error record with reason 超时", rec)
	}
	if rec.ElapsedMs < 50 {
		t.Errorf("elapsed_ms = %d, want the time spent waiting", rec.ElapsedMs)
	}
}

func TestClassifyPlayerInput_NoLogWhenGuardUnavailable(t *testing.T) {
	prev := inputGuardLoader
	inputGuardLoader = func() (*inputGuard, bool) { return nil, false }
	t.Cleanup(func() { inputGuardLoader = prev })

	if v := ClassifyPlayerInput(context.Background(), "我是作者", InputContext{}); v.Log != nil {
		t.Errorf("verdict.Log = %+v, want nil when no call was made", v.Log)
	}
}

func TestClassifyFloorNSFW_LogRecordsTheDecision(t *testing.T) {
	useFakeGuard(t, func(w http.ResponseWriter, r *http.Request) {
		withRequestID(w)
		writeJSON(w, nsfwRouteResponse(t, map[string]float64{"sexual_explicit": 0.3, "sexual_suggestive": 0.25, "non_sexual": 0.45}))
	})
	if err := models.SetSiteSetting(nsfwRouteThresholdKey, "0.6"); err != nil {
		t.Fatalf("SetSiteSetting: %v", err)
	}

	outcome, rec := classifyFloorNSFW(context.Background(), routeGctx(true, "", playerAction))
	if outcome != nsfwRouteClean || rec == nil {
		t.Fatalf("outcome=%v record=%+v, want clean with a record", outcome, rec)
	}
	if rec.Kind != models.ClassifierKindNSFWRoute || rec.Decision != models.ClassifierDecisionClean || rec.Status != models.ClassifierStatusOK {
		t.Errorf("kind/decision/status = %q/%q/%q, want nsfw_route/clean/ok", rec.Kind, rec.Decision, rec.Status)
	}
	if rec.Score < 0.549 || rec.Score > 0.551 || rec.Threshold != 0.6 || rec.Choice != "non_sexual" {
		t.Errorf("score=%v threshold=%v choice=%q, want 0.55 against 0.6 and non_sexual", rec.Score, rec.Threshold, rec.Choice)
	}
	if rec.Request.Data["player_actions"] != "[甲]: 我走向她" || rec.RequestID != testRequestID {
		t.Errorf("request=%v request_id=%q, want the state that was sent and %q", rec.Request.Data, rec.RequestID, testRequestID)
	}
	if rec.Role != "" || rec.Floor != 0 || rec.Subject != "" {
		t.Errorf("role/floor/subject = %q/%d/%q, the classifier must leave ownership to the caller", rec.Role, rec.Floor, rec.Subject)
	}

	if err := models.SetSiteSetting(nsfwRouteThresholdKey, "0.5"); err != nil {
		t.Fatalf("SetSiteSetting: %v", err)
	}
	outcome, rec = classifyFloorNSFW(context.Background(), routeGctx(true, "", playerAction))
	if outcome != nsfwRouteNSFW || rec.Decision != models.ClassifierDecisionNSFW {
		t.Errorf("outcome=%v decision=%q, want nsfw once the threshold drops to 0.5", outcome, rec.Decision)
	}
}

func TestClassifyFloorNSFW_LogRecordsFailures(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		wantOutcome  nsfwRouteOutcome
		wantDecision string
		wantError    string
	}{
		{"400 counts as a safety filter hit", http.StatusBadRequest, nsfwRouteNSFW, models.ClassifierDecisionNSFW, "HTTP 400（按安全过滤命中处理）"},
		{"401 keeps the previous director", http.StatusUnauthorized, nsfwRouteFailed, models.ClassifierDecisionFailed, "HTTP 401"},
		{"500 keeps the previous director", http.StatusInternalServerError, nsfwRouteFailed, models.ClassifierDecisionFailed, "HTTP 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useFakeGuard(t, func(w http.ResponseWriter, r *http.Request) { withRequestID(w); w.WriteHeader(tc.status) })

			outcome, rec := classifyFloorNSFW(context.Background(), routeGctx(true, "", playerAction))
			if outcome != tc.wantOutcome || rec == nil {
				t.Fatalf("outcome=%v record=%+v, want %v with a record", outcome, rec, tc.wantOutcome)
			}
			if rec.Status != models.ClassifierStatusError || rec.Decision != tc.wantDecision || rec.Error != tc.wantError {
				t.Errorf("status/decision/error = %q/%q/%q, want error/%s/%q", rec.Status, rec.Decision, rec.Error, tc.wantDecision, tc.wantError)
			}
			if rec.RequestID != testRequestID || rec.Choice != "" || rec.Score != 0 || len(rec.Probabilities.Data) != 0 {
				t.Errorf("request_id=%q choice=%q score=%v probabilities=%v, want an empty failure record", rec.RequestID, rec.Choice, rec.Score, rec.Probabilities.Data)
			}
		})
	}

	t.Run("missing answer keeps the previous director", func(t *testing.T) {
		useFakeGuard(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, `{"model":"jev-test","answers":{},"usage":{"input_tokens":1,"output_tokens":1}}`)
		})
		outcome, rec := classifyFloorNSFW(context.Background(), routeGctx(true, "", playerAction))
		if outcome != nsfwRouteFailed || rec == nil || rec.Decision != models.ClassifierDecisionFailed || rec.Error != "响应异常" {
			t.Errorf("outcome=%v record=%+v, want failed with reason 响应异常", outcome, rec)
		}
	})
}

func TestPickDirectorRole_ReturnsTheClassifierRecordOnlyWhenItClassified(t *testing.T) {
	want := &models.ClassifierLog{Kind: models.ClassifierKindNSFWRoute, Decision: models.ClassifierDecisionNSFW}
	prev := nsfwRouteClassifier
	nsfwRouteClassifier = func(context.Context, GameContext) (nsfwRouteOutcome, *models.ClassifierLog) {
		return nsfwRouteNSFW, want
	}
	t.Cleanup(func() { nsfwRouteClassifier = prev })

	role, rec := pickDirectorRole(context.Background(), routeGctx(true, "", playerAction), routeHandles(true), models.AgentRoleDirector)
	if role != models.AgentRoleDirectorNSFW || rec != want {
		t.Errorf("role=%q record=%p, want director_nsfw and the classifier's own record %p", role, rec, want)
	}

	for name, gctx := range map[string]GameContext{
		"opening":         routeGctx(true, KPFloorOpening, playerAction),
		"resume":          routeGctx(true, KPFloorResume),
		"room switch off": routeGctx(false, "", playerAction),
	} {
		if _, rec := pickDirectorRole(context.Background(), gctx, routeHandles(true), models.AgentRoleDirector); rec != nil {
			t.Errorf("%s: record = %+v, want nil because nothing was classified", name, rec)
		}
	}
}

func TestRecordRouteLog_FillsOwnershipAndHandsItToTheHook(t *testing.T) {
	var got []*models.ClassifierLog
	gctx := routeGctx(true, "", playerAction,
		PlayerAction{UserID: 2, PlayerName: "乙", Content: "我后退"},
		PlayerAction{UserID: 1, PlayerName: "甲", Content: "我再走近一步"},
		PlayerAction{IsAdmin: true, PlayerName: "KP", Content: "DEBUG: x"},
	)
	gctx.RecordClassifierLog = func(rec *models.ClassifierLog) { got = append(got, rec) }

	rec := &models.ClassifierLog{Kind: models.ClassifierKindNSFWRoute}
	gctx.recordRouteLog(rec, models.AgentRoleDirectorNSFW, 7)

	if len(got) != 1 || got[0] != rec {
		t.Fatalf("hook calls = %d, want exactly one with the same record", len(got))
	}
	if rec.Role != "director_nsfw" || rec.Floor != 7 || rec.Subject != "甲、乙、KP" {
		t.Errorf("role/floor/subject = %q/%d/%q, want director_nsfw/7/甲、乙、KP (deduplicated, admin included)", rec.Role, rec.Floor, rec.Subject)
	}

	gctx.recordRouteLog(nil, models.AgentRoleDirector, 8)
	gctx.RecordClassifierLog = nil
	gctx.recordRouteLog(&models.ClassifierLog{}, models.AgentRoleDirector, 9)
	if len(got) != 1 {
		t.Errorf("hook calls = %d, want no call for a nil record or without a hook", len(got))
	}
}

func TestRun_RecordsTheRouteClassificationForTheFloor(t *testing.T) {
	const sid = 306
	newRouteSession(t, sid, true)
	setRouteHandles(t, sid, routeProvider(routeSigDirector), routeProvider(routeSigNSFW), false)

	prev := nsfwRouteClassifier
	nsfwRouteClassifier = func(context.Context, GameContext) (nsfwRouteOutcome, *models.ClassifierLog) {
		return nsfwRouteNSFW, &models.ClassifierLog{Kind: models.ClassifierKindNSFWRoute, Decision: models.ClassifierDecisionNSFW}
	}
	t.Cleanup(func() { nsfwRouteClassifier = prev })

	var published []RoundOutputs
	var recorded []*models.ClassifierLog
	gctx := routeFloorGctx(sid, true, &published)
	gctx.RecordClassifierLog = func(rec *models.ClassifierLog) { recorded = append(recorded, rec) }
	if _, err := run(context.Background(), gctx); err != nil {
		t.Fatalf("run(): %v", err)
	}

	if len(recorded) != 1 {
		t.Fatalf("recorded = %d, want one record for the cycle", len(recorded))
	}
	if rec := recorded[0]; rec.Role != "director_nsfw" || rec.Floor != 1 || rec.Subject != "甲" {
		t.Errorf("role/floor/subject = %q/%d/%q, want director_nsfw/1/甲", rec.Role, rec.Floor, rec.Subject)
	}
	if len(published) == 0 || published[0].WriterCtx.RecordClassifierLog != nil {
		t.Error("the Writer snapshot must not carry the record hook")
	}
}

func TestDescribeClassifierError_FallsBackToTheErrorText(t *testing.T) {
	reason, requestID := describeClassifierError(context.DeadlineExceeded)
	if reason != "超时" || requestID != "" {
		t.Errorf("deadline: reason=%q requestID=%q, want 超时", reason, requestID)
	}
	if reason, _ := describeClassifierError(&testError{"boom"}); !strings.Contains(reason, "boom") {
		t.Errorf("unknown error: reason=%q, want the error text", reason)
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
