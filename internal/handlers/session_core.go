package handlers

// NOTE: 真人（gin 处理器）与房间里的 AI 陪玩（后台协程）共用同一批核心函数：提交回应 submitTurn、加入房间
// joinSessionPlayer、桌边闲聊 postOOC/saveOOC、手册笔记 createNote/saveNote、手册快照 buildHandbook。
// 核心函数不碰 *gin.Context，失败时返回 *turnError；处理器只负责解析入参，并用 respondTurnError 把它映射回
// 原来的 HTTP 状态码和错误文案。
// postOOC/createNote 是给没有 gin 上下文的调用方用的入口（管理员身份只认库里的 role）；处理器要认 JWT 里的 role，
// 所以直接调用带 isAdmin、username 参数的 saveOOC/saveNote，二者共用同一份校验、落库和广播。

import (
	"github.com/gin-gonic/gin"
)

// turnError 是核心函数的失败结果，携带处理器原本直接写给客户端的状态码和文案。
type turnError struct {
	status int
	msg    string
	// notAsked 只在「点名闸门不通过」时置位：此刻没轮到这个人发言（没被点名、点名已变化、或发言权在 KP 手里）。
	// AI 侧据此区分「点名已变化，重新观察」和真正的失败。
	notAsked bool
}

func (e *turnError) Error() string { return e.msg }

// respondTurnError 把核心函数的失败原样写成处理器原来的 JSON 响应。
func respondTurnError(c *gin.Context, terr *turnError) {
	c.JSON(terr.status, gin.H{"error": terr.msg})
}
