package models

import (
	"time"

	"gorm.io/gorm"
)

// NOTE: System One 分类器的两种调用；Kind 决定 Decision 的取值范围。
const (
	ClassifierKindInputGuard = "input_guard" // 玩家输入的越权请求分类
	ClassifierKindNSFWRoute  = "nsfw_route"  // Director 开跑前的 NSFW 路由分类
)

const (
	ClassifierDecisionBlocked = "blocked" // input_guard：拦截
	ClassifierDecisionPassed  = "passed"  // input_guard：放行（含调用失败时放行）
	ClassifierDecisionNSFW    = "nsfw"    // nsfw_route：走 director_nsfw（含 HTTP 400 按安全过滤命中处理）
	ClassifierDecisionClean   = "clean"   // nsfw_route：走 director
	ClassifierDecisionFailed  = "failed"  // nsfw_route：没拿到可用结论，沿用上个周期的角色
)

const (
	ClassifierStatusOK    = "ok"
	ClassifierStatusError = "error"
)

// ClassifierLog 是一次真正发出去的分类调用的完整记录，只给管理员在游戏页排查阈值用。
// NOTE: Request 原样存了发给第三方的背景（玩家输入、公开聊天记录、已装备物品），随房间删除，
// 房间没删时由每日回收按固定保留期清理；没有外键，新增引用方式变化时要同步 sessionChildTables。
type ClassifierLog struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
	SessionID uint      `gorm:"not null;index" json:"-"`
	Kind      string    `gorm:"not null;size:20" json:"kind"`
	// UserID 是提交输入的玩家，nsfw_route 没有单一提交者，为 0。
	UserID uint `json:"user_id"`
	// Subject 是被分类的对象：input_guard 是提交者扮演的调查员名，nsfw_route 是本周期行动的调查员名（顿号连接）。
	Subject string `json:"subject"`
	// Floor 是 nsfw_route 所在周期的编号，input_guard 为 0。
	Floor    int    `json:"floor"`
	Decision string `gorm:"size:20" json:"decision"`
	Status   string `gorm:"size:10" json:"status"`
	// Error 在 Status=error 时说明原因；HTTP 400 按安全过滤命中处理时 Decision 仍是 nsfw。
	Error     string  `json:"error"`
	Choice    string  `json:"choice"`
	Score     float64 `json:"score"`
	Threshold float64 `json:"threshold"`
	// Probabilities 是各选项概率，调用失败时为空对象而不是 null。
	Probabilities JSONField[map[string]float64] `gorm:"type:text" json:"probabilities"`
	// Request 是发给分类器的 state，键见两个分类器各自的构造函数。
	Request JSONField[map[string]string] `gorm:"type:text" json:"request"`
	// Role 是 nsfw_route 最终执行本周期的 Director 角色，input_guard 为空。
	Role      string `json:"role"`
	ElapsedMs int64  `json:"elapsed_ms"`
	RequestID string `json:"request_id"`
}

// BeforeCreate 把空的概率和请求规整成空对象，保证对外 JSON 里永远不是 null。
func (l *ClassifierLog) BeforeCreate(*gorm.DB) error {
	if l.Probabilities.Data == nil {
		l.Probabilities.Data = map[string]float64{}
	}
	if l.Request.Data == nil {
		l.Request.Data = map[string]string{}
	}
	return nil
}
