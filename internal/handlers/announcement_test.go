package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func announcementRouter() *gin.Engine {
	r := gin.New()
	pub := r.Group("/", withAuth(2, "user", "user"))
	pub.GET("/announcements", ListAnnouncements)

	adm := r.Group("/admin", withAuth(1, "admin", "admin"))
	adm.GET("/announcements", AdminListAnnouncements)
	adm.POST("/announcements", AdminCreateAnnouncement)
	adm.PUT("/announcements/:id", AdminUpdateAnnouncement)
	adm.DELETE("/announcements/:id", AdminDeleteAnnouncement)
	return r
}

func TestAdminCreateAnnouncement_DefaultsLevelAndActive(t *testing.T) {
	initTestDB(t)
	r := announcementRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/announcements", map[string]any{
		"title":   "维护通知",
		"content": "今晚 22:00 维护",
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var got models.Announcement
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Level != models.AnnouncementLevelNormal {
		t.Errorf("level = %q, want normal", got.Level)
	}
	if !got.IsActive {
		t.Errorf("is_active = false, want true (default)")
	}
	if got.CreatedBy != 1 {
		t.Errorf("created_by = %d, want 1", got.CreatedBy)
	}
}

// TestAdminCreateAnnouncement_KeepsExplicitInactive 防止 GORM 的 default 零值陷阱：
// 显式传 is_active=false 必须真的存成 false，而不是被 gorm 的 `default` 标签悄悄改回 true。
func TestAdminCreateAnnouncement_KeepsExplicitInactive(t *testing.T) {
	initTestDB(t)
	r := announcementRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/announcements", map[string]any{
		"title":     "草稿公告",
		"content":   "还没准备好",
		"is_active": false,
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var got models.Announcement
	json.NewDecoder(w.Body).Decode(&got)
	if got.IsActive {
		t.Errorf("is_active = true, want false to be preserved")
	}

	var stored models.Announcement
	if err := models.DB.First(&stored, got.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.IsActive {
		t.Errorf("stored is_active = true, want false")
	}
}

func TestAdminCreateAnnouncement_RejectsMissingTitle(t *testing.T) {
	initTestDB(t)
	r := announcementRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/announcements", map[string]any{
		"content": "没有标题",
	}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminCreateAnnouncement_RejectsInvalidLevel(t *testing.T) {
	initTestDB(t)
	r := announcementRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/announcements", map[string]any{
		"title":   "标题",
		"content": "正文",
		"level":   "critical",
	}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminCreateAnnouncement_RejectsEndsBeforeStarts(t *testing.T) {
	initTestDB(t)
	r := announcementRouter()

	now := time.Now().UTC()
	starts := now.Add(2 * time.Hour)
	ends := now.Add(time.Hour)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/admin/announcements", map[string]any{
		"title":     "标题",
		"content":   "正文",
		"starts_at": starts,
		"ends_at":   ends,
	}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminUpdateAnnouncement_NotFound(t *testing.T) {
	initTestDB(t)
	r := announcementRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/announcements/999", map[string]any{
		"title":   "标题",
		"content": "正文",
	}))

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAdminUpdateAnnouncement_ClearsEndsAt 验证有效期可以被显式清空为 null。
func TestAdminUpdateAnnouncement_ClearsEndsAt(t *testing.T) {
	initTestDB(t)
	r := announcementRouter()

	end := time.Now().UTC().Add(time.Hour)
	a := models.Announcement{Title: "旧标题", Content: "旧正文", Level: models.AnnouncementLevelNormal, IsActive: true, EndsAt: &end, CreatedBy: 1}
	if err := models.DB.Create(&a).Error; err != nil {
		t.Fatalf("seed announcement: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/announcements/"+itoa(a.ID), map[string]any{
		"title":   "新标题",
		"content": "新正文",
		"ends_at": nil,
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got models.Announcement
	json.NewDecoder(w.Body).Decode(&got)
	if got.Title != "新标题" || got.EndsAt != nil {
		t.Errorf("got title=%q endsAt=%v, want 新标题/nil", got.Title, got.EndsAt)
	}
}

func TestAdminDeleteAnnouncement(t *testing.T) {
	initTestDB(t)
	r := announcementRouter()

	a := models.Announcement{Title: "待删除", Content: "正文", Level: models.AnnouncementLevelNormal, IsActive: true, CreatedBy: 1}
	models.DB.Create(&a)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/admin/announcements/"+itoa(a.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, jsonReq("DELETE", "/admin/announcements/"+itoa(a.ID), nil))
	if w2.Code != http.StatusNotFound {
		t.Fatalf("want 404 on second delete, got %d", w2.Code)
	}
}

func TestAdminListAnnouncements_IncludesInactiveAndPaginates(t *testing.T) {
	initTestDB(t)
	r := announcementRouter()

	models.DB.Create(&models.Announcement{Title: "启用", Content: "x", Level: models.AnnouncementLevelNormal, IsActive: true, CreatedBy: 1})
	models.DB.Create(&models.Announcement{Title: "下线", Content: "x", Level: models.AnnouncementLevelNormal, IsActive: false, CreatedBy: 1})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/admin/announcements", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp PaginatedResponse[models.Announcement]
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total != 2 {
		t.Errorf("total = %d, want 2 (should include inactive)", resp.Total)
	}
}

// TestListAnnouncements_FiltersAndOrders 验证公开接口过滤下线/未开始/已过期的公告，
// 并按"置顶优先，其次创建时间倒序"排序。
func TestListAnnouncements_FiltersAndOrders(t *testing.T) {
	initTestDB(t)
	r := announcementRouter()

	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	inactive := models.Announcement{Title: "下线", Content: "x", Level: models.AnnouncementLevelNormal, IsActive: false, CreatedBy: 1, CreatedAt: now}
	notStarted := models.Announcement{Title: "未开始", Content: "x", Level: models.AnnouncementLevelNormal, IsActive: true, StartsAt: &future, CreatedBy: 1, CreatedAt: now}
	expired := models.Announcement{Title: "已过期", Content: "x", Level: models.AnnouncementLevelNormal, IsActive: true, EndsAt: &past, CreatedBy: 1, CreatedAt: now}
	older := models.Announcement{Title: "较旧公告", Content: "x", Level: models.AnnouncementLevelNormal, IsActive: true, CreatedBy: 1, CreatedAt: now.Add(-time.Minute)}
	newer := models.Announcement{Title: "较新公告", Content: "x", Level: models.AnnouncementLevelNormal, IsActive: true, CreatedBy: 1, CreatedAt: now}
	pinned := models.Announcement{Title: "置顶公告", Content: "x", Level: models.AnnouncementLevelNormal, IsActive: true, IsPinned: true, CreatedBy: 1, CreatedAt: now.Add(-2 * time.Minute)}

	for _, a := range []*models.Announcement{&inactive, &notStarted, &expired, &older, &newer, &pinned} {
		if err := models.DB.Create(a).Error; err != nil {
			t.Fatalf("seed announcement %q: %v", a.Title, err)
		}
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/announcements", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got []models.Announcement
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got) != 3 {
		titles := make([]string, len(got))
		for i, a := range got {
			titles[i] = a.Title
		}
		t.Fatalf("got %d announcements %v, want 3 (置顶公告/较新公告/较旧公告)", len(got), titles)
	}
	wantOrder := []string{"置顶公告", "较新公告", "较旧公告"}
	for i, want := range wantOrder {
		if got[i].Title != want {
			t.Errorf("position %d = %q, want %q", i, got[i].Title, want)
		}
	}
}

func itoa(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
