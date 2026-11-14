// NOTE: 线上模组：管理员从 cnmods 下载模组压缩包，服务端提取正文并做版权声明筛查后存档，
// 确认后再交给故事编译流水线。下载只经受限的 onlinemod 客户端（主机白名单、体积上限）。
package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/logging"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/onlinemod"
	"gorm.io/gorm"
)

var onlineLog = logging.For("online_module")

// onlineSource 是 handlers 用到的抓取能力，测试里替换成假实现，避免联网。
type onlineSource interface {
	List(ctx context.Context, q onlinemod.ListQuery) (onlinemod.ListPage, error)
	ResolveLanzou(ctx context.Context, shareURL string) (direct, filename string, err error)
	Download(ctx context.Context, direct string, maxBytes int64) ([]byte, error)
}

var (
	onlineClient        onlineSource = onlinemod.NewClient()
	onlineValidShareURL              = onlinemod.ValidShareURL
)

const (
	onlineArchiveMaxBytes    = 50 << 20
	onlineCompileMaxBytes    = 512 * 1024
	onlineDownloadTimeout    = 4 * time.Minute
	onlineListTimeout        = 20 * time.Second
	onlineDownloadStaleAfter = 10 * time.Minute
	onlinePreviewRunes       = 300
	onlineTitleMaxRunes      = 100
)

// flexString 接受来源站忽略类型的字段：字符串、数字、null 都收成字符串。
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*f = flexString(strings.TrimSpace(v))
		return nil
	}
	*f = flexString(s)
	return nil
}

// flexInt 接受数字、数字字符串和 null，无法解析的按 0 处理。
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexInt(n)
	return nil
}

type onlineDownloadReq struct {
	KeyID           flexString `json:"keyId"`
	Title           string     `json:"title"`
	Article         string     `json:"article"`
	URL             string     `json:"url"`
	ModuleAge       string     `json:"moduleAge"`
	Structure       string     `json:"structure"`
	MinAmount       flexInt    `json:"minAmount"`
	MaxAmount       flexInt    `json:"maxAmount"`
	MinDuration     flexInt    `json:"minDuration"`
	MaxDuration     flexInt    `json:"maxDuration"`
	ModuleVersion   string     `json:"moduleVersion"`
	OccurrencePlace string     `json:"occurrencePlace"`
	Opinion         string     `json:"opinion"`
}

func (r onlineDownloadReq) meta() models.OnlineModuleMeta {
	return models.OnlineModuleMeta{
		ModuleAge:   r.ModuleAge,
		Structure:   r.Structure,
		MinAmount:   int(r.MinAmount),
		MaxAmount:   int(r.MaxAmount),
		MinDuration: int(r.MinDuration),
		MaxDuration: int(r.MaxDuration),
		Version:     r.ModuleVersion,
		Place:       r.OccurrencePlace,
		Opinion:     r.Opinion,
	}
}

type onlineLocalRef struct {
	ID         uint   `json:"id"`
	Status     string `json:"status"`
	ScenarioID *uint  `json:"scenario_id"`
}

type onlineBrowseItem struct {
	onlinemod.CnmodsItem
	Local *onlineLocalRef `json:"local"`
}

type onlineBrowseResponse struct {
	Items         []onlineBrowseItem `json:"items"`
	Page          int                `json:"page"`
	TotalPages    int                `json:"total_pages"`
	TotalElements int                `json:"total_elements"`
}

// onlineDocRow 带上正文前缀预览，列表和详情不读整篇正文。
type onlineDocRow struct {
	models.OnlineModuleDocument
	Preview string `gorm:"column:preview" json:"preview"`
}

type onlineModuleDetail struct {
	Module    models.OnlineModule `json:"module"`
	Documents []onlineDocRow      `json:"documents"`
}

type onlineDocFull struct {
	models.OnlineModuleDocument
	Text string `json:"text"`
}

type onlineCompileReq struct {
	DocumentID uint   `json:"document_id" binding:"required"`
	Name       string `json:"name"`
}

func onlineModuleFromParam(c *gin.Context) (models.OnlineModule, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "线上模组ID无效"})
		return models.OnlineModule{}, false
	}
	var m models.OnlineModule
	if err := models.DB.First(&m, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "线上模组不存在"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询线上模组失败"})
		}
		return models.OnlineModule{}, false
	}
	return m, true
}

func loadOnlineDocRows(moduleID uint) ([]onlineDocRow, error) {
	rows := make([]onlineDocRow, 0)
	err := models.DB.Model(&models.OnlineModuleDocument{}).
		Select("id, online_module_id, path, kind, runes, candidate, license_hits, created_at, substr(text, 1, ?) AS preview", onlinePreviewRunes).
		Where("online_module_id = ?", moduleID).
		Order("runes DESC, id ASC").
		Find(&rows).Error
	for i := range rows {
		if rows[i].LicenseHits.Data == nil {
			rows[i].LicenseHits.Data = []string{}
		}
	}
	return rows, err
}

func respondOnlineDetail(c *gin.Context, m models.OnlineModule) {
	rows, err := loadOnlineDocRows(m.ID)
	if err != nil {
		onlineLog.Error("load online module documents failed", "id", m.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询文档失败"})
		return
	}
	c.JSON(http.StatusOK, onlineModuleDetail{Module: m, Documents: rows})
}

// OnlineModuleBrowse 代理 cnmods 列表，并附上每条在本地的状态。
func OnlineModuleBrowse(c *gin.Context) {
	page, ok := parsePositiveIntQuery(c, "page", 1)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page must be a positive integer"})
		return
	}
	title := strings.TrimSpace(c.Query("title"))
	if len([]rune(title)) > onlineTitleMaxRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "搜索词过长"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), onlineListTimeout)
	defer cancel()
	list, err := onlineClient.List(ctx, onlinemod.ListQuery{Page: page, Title: title})
	if err != nil {
		onlineLog.Warn("browse cnmods failed", "err", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "访问 cnmods 失败: " + err.Error()})
		return
	}

	keys := make([]string, 0, len(list.Items))
	for _, it := range list.Items {
		keys = append(keys, it.KeyID)
	}
	local := map[string]onlineLocalRef{}
	if len(keys) > 0 {
		var rows []models.OnlineModule
		if err := models.DB.Select("id, source_key, status, scenario_id").
			Where("source = ? AND source_key IN ?", models.OnlineModuleSourceCnmods, keys).
			Find(&rows).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询本地状态失败"})
			return
		}
		for _, r := range rows {
			local[r.SourceKey] = onlineLocalRef{ID: r.ID, Status: r.Status, ScenarioID: r.ScenarioID}
		}
	}

	items := make([]onlineBrowseItem, 0, len(list.Items))
	for _, it := range list.Items {
		entry := onlineBrowseItem{CnmodsItem: it}
		if ref, found := local[it.KeyID]; found {
			ref := ref
			entry.Local = &ref
		}
		items = append(items, entry)
	}
	c.JSON(http.StatusOK, onlineBrowseResponse{
		Items:         items,
		Page:          list.Page,
		TotalPages:    list.TotalPages,
		TotalElements: list.TotalElements,
	})
}

// OnlineModuleList 返回本地已记录的线上模组，不含正文。
func OnlineModuleList(c *gin.Context) {
	page, pageSize, ok := parseAdminPagination(c)
	if !ok {
		return
	}
	var total int64
	if err := models.DB.Model(&models.OnlineModule{}).Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询线上模组总数失败"})
		return
	}
	items := make([]models.OnlineModule, 0)
	if err := models.DB.Order("updated_at DESC, id DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询线上模组列表失败"})
		return
	}
	c.JSON(http.StatusOK, newPaginatedResponse(items, page, pageSize, total))
}

// OnlineModuleGet 返回条目详情和文档列表（每篇只带预览）。
func OnlineModuleGet(c *gin.Context) {
	m, ok := onlineModuleFromParam(c)
	if !ok {
		return
	}
	respondOnlineDetail(c, m)
}

// OnlineModuleDocument 返回单篇文档全文。
func OnlineModuleDocument(c *gin.Context) {
	m, ok := onlineModuleFromParam(c)
	if !ok {
		return
	}
	docID, err := strconv.ParseUint(c.Param("docId"), 10, 64)
	if err != nil || docID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文档ID无效"})
		return
	}
	var doc models.OnlineModuleDocument
	if err := models.DB.Where("id = ? AND online_module_id = ?", docID, m.ID).First(&doc).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
		return
	}
	if doc.LicenseHits.Data == nil {
		doc.LicenseHits.Data = []string{}
	}
	c.JSON(http.StatusOK, onlineDocFull{OnlineModuleDocument: doc, Text: doc.Text})
}

// claimOnlineModule 用条件 UPDATE 抢占下载状态：已有进行中的任务（且未超时）时返回 false。
func claimOnlineModule(id uint) (bool, error) {
	cutoff := time.Now().Add(-onlineDownloadStaleAfter)
	res := models.DB.Model(&models.OnlineModule{}).
		Where("id = ? AND (status <> ? OR updated_at < ?)", id, models.OnlineModuleStatusDownloading, cutoff).
		Updates(map[string]any{"status": models.OnlineModuleStatusDownloading, "error": ""})
	return res.RowsAffected == 1, res.Error
}

func setOnlineModuleState(id uint, status, message string) {
	if err := models.DB.Model(&models.OnlineModule{}).Where("id = ?", id).
		Updates(map[string]any{"status": status, "error": message}).Error; err != nil {
		onlineLog.Error("update online module state failed", "id", id, "err", err)
	}
}

func onlineFailureMessage(err error) string {
	switch {
	case errors.Is(err, onlinemod.ErrChallenge):
		return "下载被蓝奏云拦截，请打开分享页手动下载后上传"
	case errors.Is(err, onlinemod.ErrUnsupportedArchive):
		return "不支持 rar / 7z 压缩包，请解压后重新打包成 zip 再上传"
	case errors.Is(err, onlinemod.ErrNoDocument):
		return "压缩包里没有可提取的文档（支持 docx、pdf、txt、md）"
	case errors.Is(err, context.DeadlineExceeded):
		return "下载超时，请稍后重试或手动下载后上传"
	default:
		return "下载或提取失败: " + err.Error()
	}
}

// ingestOnlineArchive 提取压缩包正文、做版权筛查，并在一个事务里替换该条目的全部文档。
// 内容变了，之前的版权放行记录一并清空。
func ingestOnlineArchive(id uint, name string, data []byte) error {
	docs, err := onlinemod.Extract(data, name)
	if err != nil {
		return err
	}
	rows := make([]models.OnlineModuleDocument, 0, len(docs))
	for _, d := range docs {
		hits := onlinemod.ScanLicense(d.Text)
		if hits == nil {
			hits = []string{}
		}
		rows = append(rows, models.OnlineModuleDocument{
			OnlineModuleID: id,
			Path:           d.Path,
			Kind:           d.Kind,
			Runes:          d.Runes,
			Candidate:      d.Candidate,
			Text:           d.Text,
			LicenseHits:    models.JSONField[[]string]{Data: hits},
		})
	}
	sum := sha256.Sum256(data)
	return models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("online_module_id = ?", id).Delete(&models.OnlineModuleDocument{}).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			if err := tx.CreateInBatches(rows, 20).Error; err != nil {
				return err
			}
		}
		res := tx.Model(&models.OnlineModule{}).Where("id = ?", id).Updates(map[string]any{
			"status":              models.OnlineModuleStatusExtracted,
			"error":               "",
			"archive_name":        name,
			"archive_size":        len(data),
			"archive_sha256":      hex.EncodeToString(sum[:]),
			"license_override_by": nil,
			"license_override_at": nil,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errors.New("线上模组记录已被删除")
		}
		return nil
	})
}

// upsertOnlineModule 按来源站条目 id 创建或刷新展示信息，不动下载状态。
func upsertOnlineModule(req onlineDownloadReq) (models.OnlineModule, error) {
	meta := models.JSONField[models.OnlineModuleMeta]{Data: req.meta()}
	author := strings.TrimSpace(req.Article)
	for attempt := 0; attempt < 2; attempt++ {
		var m models.OnlineModule
		err := models.DB.Where("source = ? AND source_key = ?", models.OnlineModuleSourceCnmods, string(req.KeyID)).First(&m).Error
		if err == nil {
			// NOTE: 用 UpdateColumns 不刷新 updated_at：抢占下载状态时要靠它判断下载是否超时。
			err = models.DB.Model(&m).UpdateColumns(map[string]any{
				"title":     req.Title,
				"author":    author,
				"share_url": req.URL,
				"meta":      meta,
			}).Error
			return m, err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return models.OnlineModule{}, err
		}
		m = models.OnlineModule{
			Source:    models.OnlineModuleSourceCnmods,
			SourceKey: string(req.KeyID),
			Title:     req.Title,
			Author:    author,
			ShareURL:  req.URL,
			Meta:      meta,
			Status:    models.OnlineModuleStatusPending,
		}
		if err := models.DB.Create(&m).Error; err == nil {
			return m, nil
		}
		// NOTE: 并发创建同一条目会撞唯一索引，重查一次即可拿到对方创建的行。
	}
	return models.OnlineModule{}, errors.New("创建线上模组记录失败")
}

// OnlineModuleDownload 同步完成「解析分享页 → 下载 → 提取 → 版权筛查 → 存档」。
// 下载或提取失败不是请求错误：返回 200，由条目的 status / error 说明原因，界面提示手动上传。
func OnlineModuleDownload(c *gin.Context) {
	var req onlineDownloadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.KeyID == "" || req.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少条目 id 或标题"})
		return
	}
	if !onlineValidShareURL(req.URL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分享链接不在允许的下载站范围内"})
		return
	}

	m, err := upsertOnlineModule(req)
	if err != nil {
		onlineLog.Error("upsert online module failed", "key", req.KeyID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存线上模组记录失败"})
		return
	}
	claimed, err := claimOnlineModule(m.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新下载状态失败"})
		return
	}
	if !claimed {
		c.JSON(http.StatusConflict, gin.H{"error": "该模组正在下载中"})
		return
	}

	// NOTE: 用脱离请求的 ctx：客户端中途断开也要把状态落完，不留下 downloading。
	ctx, cancel := context.WithTimeout(context.Background(), onlineDownloadTimeout)
	defer cancel()
	if err := runOnlineDownload(ctx, m); err != nil {
		onlineLog.Warn("online module download failed", "id", m.ID, "title", m.Title, "err", err)
		setOnlineModuleState(m.ID, models.OnlineModuleStatusFailed, onlineFailureMessage(err))
	}

	var fresh models.OnlineModule
	if err := models.DB.First(&fresh, m.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取线上模组记录失败"})
		return
	}
	c.JSON(http.StatusOK, fresh)
}

func runOnlineDownload(ctx context.Context, m models.OnlineModule) error {
	direct, filename, err := onlineClient.ResolveLanzou(ctx, m.ShareURL)
	if err != nil {
		return err
	}
	data, err := onlineClient.Download(ctx, direct, onlineArchiveMaxBytes)
	if err != nil {
		return err
	}
	if strings.TrimSpace(filename) == "" {
		filename = "download.zip"
	}
	return ingestOnlineArchive(m.ID, filename, data)
}

// OnlineModuleUploadArchive 是自动下载失败后的兜底：管理员手动下载压缩包后上传，走同一套提取流程。
// 提取失败时恢复原有状态并返回 422，不破坏之前已提取的内容。
func OnlineModuleUploadArchive(c *gin.Context) {
	m, ok := onlineModuleFromParam(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, onlineArchiveMaxBytes+(1<<20))
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传压缩包或文档(字段名 file)，大小不超过 50MB"})
		return
	}
	if fh.Size > onlineArchiveMaxBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "文件超过 50MB"})
		return
	}
	f, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "打开上传文件失败"})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, onlineArchiveMaxBytes+1))
	if err != nil || int64(len(data)) > onlineArchiveMaxBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取上传文件失败或文件超过 50MB"})
		return
	}

	claimed, err := claimOnlineModule(m.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新下载状态失败"})
		return
	}
	if !claimed {
		c.JSON(http.StatusConflict, gin.H{"error": "该模组正在下载中"})
		return
	}
	if err := ingestOnlineArchive(m.ID, fh.Filename, data); err != nil {
		onlineLog.Warn("online module archive upload failed", "id", m.ID, "err", err)
		setOnlineModuleState(m.ID, m.Status, m.Error)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": onlineFailureMessage(err)})
		return
	}

	var fresh models.OnlineModule
	if err := models.DB.First(&fresh, m.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取线上模组记录失败"})
		return
	}
	respondOnlineDetail(c, fresh)
}

// OnlineModuleLicenseOverride 记录管理员放行了版权筛查命中的内容。只有确实存在命中时才可放行。
func OnlineModuleLicenseOverride(c *gin.Context) {
	m, ok := onlineModuleFromParam(c)
	if !ok {
		return
	}
	if m.Status != models.OnlineModuleStatusExtracted {
		c.JSON(http.StatusConflict, gin.H{"error": "尚未提取正文"})
		return
	}
	rows, err := loadOnlineDocRows(m.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询文档失败"})
		return
	}
	hit := false
	for _, r := range rows {
		if len(r.LicenseHits.Data) > 0 {
			hit = true
			break
		}
	}
	if !hit {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有需要放行的版权筛查命中"})
		return
	}

	now := time.Now()
	uid := c.GetUint("user_id")
	if err := models.DB.Model(&models.OnlineModule{}).Where("id = ?", m.ID).
		Updates(map[string]any{"license_override_by": uid, "license_override_at": now}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "记录放行失败"})
		return
	}
	onlineLog.Info("online module license override", "id", m.ID, "title", m.Title, "user_id", uid)

	var fresh models.OnlineModule
	if err := models.DB.First(&fresh, m.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取线上模组记录失败"})
		return
	}
	c.JSON(http.StatusOK, fresh)
}

// OnlineModuleCompile 把选中的一篇正文交给故事编译流水线（SSE）。所有校验都在发出 SSE 头之前完成。
func OnlineModuleCompile(c *gin.Context) {
	m, ok := onlineModuleFromParam(c)
	if !ok {
		return
	}
	var req onlineCompileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if m.Status != models.OnlineModuleStatusExtracted {
		c.JSON(http.StatusConflict, gin.H{"error": "尚未提取正文"})
		return
	}
	var doc models.OnlineModuleDocument
	if err := models.DB.Where("id = ? AND online_module_id = ?", req.DocumentID, m.ID).First(&doc).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
		return
	}
	if len(doc.LicenseHits.Data) > 0 && m.LicenseOverrideAt == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "所选文档命中版权声明筛查，需管理员确认放行后才能编译"})
		return
	}
	if len(doc.Text) > onlineCompileMaxBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "正文超过 500KB，无法编译"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = m.Title
	}
	if len([]rune(name)) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模组名称过长"})
		return
	}
	var dup int64
	if err := models.DB.Model(&models.Scenario{}).Where("name = ?", name).Count(&dup).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模组失败"})
		return
	}
	if dup > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("已存在同名模组「%s」，请换一个名称", name)})
		return
	}

	author := strings.TrimSpace(m.Author)
	if author == "" {
		author = "cnmods"
	} else {
		author += "（cnmods）"
	}
	moduleID := m.ID
	streamCompileStory(c, agent.CompileStoryRequest{StoryDocument: doc.Text, Name: name},
		func(s *models.Scenario) {
			s.Name = name
			s.Author = author
		},
		func(s *models.Scenario) {
			if err := models.DB.Model(&models.OnlineModule{}).Where("id = ?", moduleID).
				Update("scenario_id", s.ID).Error; err != nil {
				onlineLog.Error("backfill online module scenario id failed", "id", moduleID, "scenario_id", s.ID, "err", err)
			}
		})
}

// OnlineModuleDelete 删除条目和提取出的文档，不影响已编译入库的模组。
func OnlineModuleDelete(c *gin.Context) {
	m, ok := onlineModuleFromParam(c)
	if !ok {
		return
	}
	if m.Status == models.OnlineModuleStatusDownloading && time.Since(m.UpdatedAt) < onlineDownloadStaleAfter {
		c.JSON(http.StatusConflict, gin.H{"error": "该模组正在下载中，暂时不能删除"})
		return
	}
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("online_module_id = ?", m.ID).Delete(&models.OnlineModuleDocument{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.OnlineModule{}, m.ID).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除线上模组失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
