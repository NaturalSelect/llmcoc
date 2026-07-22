package handlers

// PaginatedResponse 是所有列表类接口统一的分页信封结构。
type PaginatedResponse[T any] struct {
	Items      []T   `json:"items"`
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

// newPaginatedResponse 根据总数计算 total_pages 并封装成统一的分页响应。
func newPaginatedResponse[T any](items []T, page, pageSize int, total int64) PaginatedResponse[T] {
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	return PaginatedResponse[T]{
		Items:      items,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
	}
}
