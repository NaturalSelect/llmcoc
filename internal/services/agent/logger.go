// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"fmt"
	"time"

	"github.com/llmcoc/server/internal/logging"
)

// alog 是 agent 包内共享的 logger。命名避开 "log" 是因为包内其他尚未迁移的文件
// 仍 import 标准库 "log"，两者同名会在这些文件里报 "log already declared" 编译错误
// （Go 不允许包级声明与任一文件的 import 名冲突），这样可以逐文件迁移，不需要一次改完整个包。
var alog = logging.For("agent")

// debugf writes a Debug 级别的追踪日志，由 LOG_LEVEL 环境变量控制是否输出。
// tag 作为结构化字段而非拼进消息体，便于按 tag 过滤检索。
func debugf(tag, format string, args ...any) {
	alog.Debug(fmt.Sprintf(format, args...), "tag", tag)
}

// timedDebug returns a function that logs elapsed time when called.
// Usage:
//
//	done := timedDebug("KP", "Chat session=%d iter=%d", sessionID, iter)
//	defer done()
func timedDebug(tag, format string, args ...any) func() {
	label := fmt.Sprintf(format, args...)
	start := time.Now()
	return func() {
		alog.Debug(label, "tag", tag, "elapsed_ms", float64(time.Since(start).Microseconds())/1000)
	}
}
