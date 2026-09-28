package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

// maxVisibleAnnouncements 是首页公告列表的安全上限,防止公告堆积后单次响应过大。
const maxVisibleAnnouncements = 50

// announcementOrder 是公告列表的通用排序规则:置顶优先,其次按创建时间倒序。
const announcementOrder = "is_pinned DESC, created_at DESC"

type announcementRequest struct {
	Title    string                   `json:"title" binding:"required,max=200"`
	Content  string                   `json:"content" binding:"required"`
	Level    models.AnnouncementLevel `json:"level" binding:"omitempty,oneof=normal important warning"`
	IsPinned bool                     `json:"is_pinned"`
	IsActive *bool                    `json:"is_active"`
	StartsAt *time.Time               `json:"starts_at"`
	EndsAt   *time.Time               `json:"ends_at"`
}

// validate 检查请求里跨字段的业务约束(标题非空白、有效期顺序)。
func (r announcementRequest) validate() error {
	if strings.TrimSpace(r.Title) == "" {
		return errors.New("标题不能为空")
	}
	if r.StartsAt != nil && r.EndsAt != nil && !r.EndsAt.After(*r.StartsAt) {
		return errors.New("结束时间必须晚于开始时间")
	}
	return nil
}

// normalized 返回填充好默认值、时间统一转为 UTC 的请求副本。
// SQLite 按字符串比较时间,写入和查询必须用同一时区,否则跨时区部署会出现边界误判。
func (r announcementRequest) normalized() announcementRequest {
	if r.Level == "" {
		r.Level = models.AnnouncementLevelNormal
	}
	if r.IsActive == nil {
		active := true
		r.IsActive = &active
	}
	if r.StartsAt != nil {
		utc := r.StartsAt.UTC()
		r.StartsAt = &utc
	}
	if r.EndsAt != nil {
		utc := r.EndsAt.UTC()
		r.EndsAt = &utc
	}
	return r
}

// ListAnnouncements handles GET /announcements.
// 返回当前对所有登录用户可见的公告:已启用且当前时间落在有效期内。
func ListAnnouncements(c *gin.Context) {
	now := time.Now().UTC()
	announcements := make([]models.Announcement, 0)
	if err := models.DB.
		Where("is_active = ?", true).
		Where("starts_at IS NULL OR starts_at <= ?", now).
		Where("ends_at IS NULL OR ends_at > ?", now).
		Order(announcementOrder).
		Limit(maxVisibleAnnouncements).
		Find(&announcements).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询公告失败"})
		return
	}
	c.JSON(http.StatusOK, announcements)
}

// AdminListAnnouncements handles GET /admin/announcements,分页返回全部公告(含下线和过期的)。
func AdminListAnnouncements(c *gin.Context) {
	page, pageSize, ok := parseAdminPagination(c)
	if !ok {
		return
	}

	var total int64
	if err := models.DB.Model(&models.Announcement{}).Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询公告总数失败"})
		return
	}

	announcements := make([]models.Announcement, 0)
	if err := models.DB.
		Order(announcementOrder).
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&announcements).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询公告列表失败"})
		return
	}

	c.JSON(http.StatusOK, newPaginatedResponse(announcements, page, pageSize, total))
}

// AdminCreateAnnouncement handles POST /admin/announcements.
func AdminCreateAnnouncement(c *gin.Context) {
	var req announcementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := req.validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req = req.normalized()

	a := models.Announcement{
		Title:     req.Title,
		Content:   req.Content,
		Level:     req.Level,
		IsPinned:  req.IsPinned,
		IsActive:  *req.IsActive,
		StartsAt:  req.StartsAt,
		EndsAt:    req.EndsAt,
		CreatedBy: c.GetUint("user_id"),
	}
	if err := models.DB.Create(&a).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败:" + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, a)
}

// AdminUpdateAnnouncement handles PUT /admin/announcements/:id。
// 前端表单每次都会提交全部字段,因此这里整体替换所有可编辑字段(包括清空有效期)。
func AdminUpdateAnnouncement(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效ID"})
		return
	}

	var a models.Announcement
	if err := models.DB.First(&a, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "公告不存在"})
		return
	}

	var req announcementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := req.validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req = req.normalized()

	updates := map[string]interface{}{
		"title":     req.Title,
		"content":   req.Content,
		"level":     req.Level,
		"is_pinned": req.IsPinned,
		"is_active": *req.IsActive,
		"starts_at": req.StartsAt,
		"ends_at":   req.EndsAt,
	}
	if err := models.DB.Model(&a).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	models.DB.First(&a, id)
	c.JSON(http.StatusOK, a)
}

// AdminDeleteAnnouncement handles DELETE /admin/announcements/:id。
func AdminDeleteAnnouncement(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效ID"})
		return
	}

	result := models.DB.Delete(&models.Announcement{}, id)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "公告不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}
