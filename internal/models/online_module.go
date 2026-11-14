// NOTE: 线上模组：记录从第三方模组站（cnmods）下载并提取出的正文，供管理员确认后再交给编译流水线。
package models

import "time"

const (
	OnlineModuleSourceCnmods = "cnmods"

	OnlineModuleStatusPending     = "pending"
	OnlineModuleStatusDownloading = "downloading"
	OnlineModuleStatusExtracted   = "extracted"
	OnlineModuleStatusFailed      = "failed"
)

// OnlineModuleMeta 保存来源站列表接口返回的展示信息，原样留存不参与逻辑。
type OnlineModuleMeta struct {
	ModuleAge   string `json:"module_age,omitempty"`
	Structure   string `json:"structure,omitempty"`
	MinAmount   int    `json:"min_amount,omitempty"`
	MaxAmount   int    `json:"max_amount,omitempty"`
	MinDuration int    `json:"min_duration,omitempty"`
	MaxDuration int    `json:"max_duration,omitempty"`
	Version     string `json:"version,omitempty"`
	Place       string `json:"place,omitempty"`
	Opinion     string `json:"opinion,omitempty"`
}

type OnlineModule struct {
	ID uint `gorm:"primaryKey;autoIncrement" json:"id"`
	// NOTE: (Source, SourceKey) 联合唯一，SourceKey 是来源站的条目 id。
	Source        string                      `gorm:"not null;size:32;uniqueIndex:idx_online_module_source_key" json:"source"`
	SourceKey     string                      `gorm:"not null;size:64;uniqueIndex:idx_online_module_source_key" json:"source_key"`
	Title         string                      `gorm:"not null;size:200" json:"title"`
	Author        string                      `gorm:"size:200" json:"author"`
	ShareURL      string                      `gorm:"size:500" json:"share_url"`
	Meta          JSONField[OnlineModuleMeta] `gorm:"type:text" json:"meta"`
	Status        string                      `gorm:"not null;size:16;default:pending;index" json:"status"`
	Error         string                      `gorm:"type:text" json:"error"`
	ArchiveName   string                      `gorm:"size:300" json:"archive_name"`
	ArchiveSize   int64                       `json:"archive_size"`
	ArchiveSHA256 string                      `gorm:"size:64" json:"archive_sha256"`
	// NOTE: 版权筛查命中后管理员放行的记录；内容变化（重新下载或上传）时必须清空，需要重新确认。
	LicenseOverrideBy *uint      `json:"license_override_by"`
	LicenseOverrideAt *time.Time `json:"license_override_at"`
	ScenarioID        *uint      `json:"scenario_id"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type OnlineModuleDocument struct {
	ID             uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	OnlineModuleID uint   `gorm:"not null;index" json:"online_module_id"`
	Path           string `gorm:"size:500" json:"path"`
	Kind           string `gorm:"size:16" json:"kind"`
	Runes          int    `json:"runes"`
	Candidate      bool   `json:"candidate"`
	// NOTE: OCR 表示正文来自 OCR 服务（PDF 本地抽取为空或乱码）；Garbled 表示正文疑似乱码且没能修复，不可编译。
	OCR         bool                `json:"ocr"`
	Garbled     bool                `json:"garbled"`
	Text        string              `gorm:"type:text" json:"-"`
	LicenseHits JSONField[[]string] `gorm:"type:text" json:"license_hits"`
	CreatedAt   time.Time           `json:"created_at"`
}
