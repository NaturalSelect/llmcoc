// NOTE: 分类记录的构造辅助：两个分类器每次真正调用 System One 后，把发出的 state、阈值和结果收进 models.ClassifierLog，
// 由 handler 落库并推给管理员；这里只造数据，不碰数据库，也不影响放行或路由的结论。
package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	typesafe "github.com/stacklok/typesafe-go"

	"github.com/llmcoc/server/internal/models"
)

const classifierProtocolErrorReason = "响应异常"

func newClassifierLog(kind string, state map[string]string, threshold float64) *models.ClassifierLog {
	return &models.ClassifierLog{
		Kind:          kind,
		Status:        models.ClassifierStatusOK,
		Threshold:     threshold,
		Request:       models.JSONField[map[string]string]{Data: state},
		Probabilities: models.JSONField[map[string]float64]{Data: map[string]float64{}},
	}
}

// failClassifierLog 把记录标成调用失败；此时没有选项和概率，Score 保持 0。
func failClassifierLog(l *models.ClassifierLog, reason, requestID string, start time.Time) {
	l.Status = models.ClassifierStatusError
	l.Error = reason
	l.RequestID = requestID
	l.ElapsedMs = time.Since(start).Milliseconds()
}

// answerClassifierLog 把分类器的回答写进记录；概率拷贝一份，记录推给订阅者后不能再被改动。
func answerClassifierLog(l *models.ClassifierLog, ans typesafe.ChoiceAnswer, requestID string, start time.Time) {
	l.Choice = ans.Choice
	l.RequestID = requestID
	l.ElapsedMs = time.Since(start).Milliseconds()
	probs := make(map[string]float64, len(ans.Probabilities))
	for name, p := range ans.Probabilities {
		probs[name] = p
	}
	l.Probabilities.Data = probs
}

// describeClassifierError 给管理员看的失败原因。typesafe 的错误按设计不带请求和响应正文，可以直接展示。
func describeClassifierError(err error) (reason, requestID string) {
	var apiErr *typesafe.APIError
	var protoErr *typesafe.ProtocolError
	switch {
	case errors.As(err, &apiErr):
		return fmt.Sprintf("HTTP %d", apiErr.StatusCode), apiErr.RequestID
	case errors.As(err, &protoErr):
		return classifierProtocolErrorReason, protoErr.RequestID
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, typesafe.ErrAttemptTimeout):
		return "超时", ""
	default:
		return err.Error(), ""
	}
}
