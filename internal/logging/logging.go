// NOTE: Package logging 是全项目唯一的日志配置入口。它在 init() 阶段完成
// slog.SetDefault，Go 保证被导入包先于 main 初始化，因此任何包都可以安全地
// 用包级变量持有 For(component) 返回的 logger，而不会捕获到未配置的 default logger。
// slog.SetDefault 同时接管标准库 log 包的输出，所以迁移可以增量进行：尚未
// 迁移到 slog 的 log.Printf 调用会自动流经同一个 handler，获得统一的输出格式。
package logging

import (
	"log/slog"
	"os"
	"strings"
)

var level = new(slog.LevelVar)

func init() {
	level.Set(parseLevel(os.Getenv("LOG_LEVEL")))
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey && len(groups) == 0 {
				a.Value = slog.StringValue(a.Value.Time().Format("15:04:05.000"))
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))
}

// parseLevel 把 LOG_LEVEL 环境变量解析成 slog.Level；非法或未设置时回落 Debug，
// 可观测性诉求优先于默认安静，需要收窄输出时显式设置 LOG_LEVEL=info/warn/error。
func parseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelDebug
	}
}

// For 返回带 component 字段的 logger，组件名沿用原来的 [xxx] 方括号前缀命名习惯。
func For(component string) *slog.Logger {
	return slog.Default().With("component", component)
}

// Level 暴露当前生效的日志级别，供测试断言使用。
func Level() *slog.LevelVar {
	return level
}
