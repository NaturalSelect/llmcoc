// NOTE: cnmods.go 实现 cnmods 公开列表接口的访问与解码。来源站字段类型不稳定
// （keyId 可能是数字或字符串，数字字段可能是字符串或 null），所以解码走私有的宽松类型，
// 对外的 CnmodsItem 保持普通结构体。
package onlinemod

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// NOTE: ListQuery 的 ModuleType 为空时用 COC，Page 小于 1 时按 1。
type ListQuery struct {
	Page       int
	Title      string
	ModuleType string
}

// NOTE: CnmodsItem 的 json 标签沿用来源站的 camelCase 字段名，handlers 直接用它绑定请求体并回传。
// 不要给它加 UnmarshalJSON：被其它结构体内嵌时方法会被提升，劫持外层结构的解码。
type CnmodsItem struct {
	KeyID           string `json:"keyId"`
	Title           string `json:"title"`
	Article         string `json:"article"`
	ModuleType      string `json:"moduleType"`
	ModuleAge       string `json:"moduleAge"`
	Structure       string `json:"structure"`
	MinAmount       int    `json:"minAmount"`
	MaxAmount       int    `json:"maxAmount"`
	MinDuration     int    `json:"minDuration"`
	MaxDuration     int    `json:"maxDuration"`
	FreeLevel       string `json:"freeLevel"`
	ReleaseDate     string `json:"releaseDate"`
	URL             string `json:"url"`
	Opinion         string `json:"opinion"`
	ModuleVersion   string `json:"moduleVersion"`
	OccurrencePlace string `json:"occurrencePlace"`
	Original        string `json:"original"`
}

type ListPage struct {
	Items         []CnmodsItem
	Page          int
	TotalPages    int
	TotalElements int
}

// flexString 接受字符串、数字、布尔和 null，统一成字符串。
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	s := bytes.TrimSpace(b)
	switch {
	case len(s) == 0 || string(s) == "null":
		*f = ""
	case s[0] == '"':
		var v string
		if err := json.Unmarshal(s, &v); err != nil {
			return err
		}
		*f = flexString(v)
	default:
		// NOTE: 数字、布尔以及意外的对象/数组都保留原始文本，避免一个怪字段让整页解析失败。
		*f = flexString(s)
	}
	return nil
}

// flexInt 接受数字、数字字符串和 null；无法解析的值按 0 处理。
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if s[0] == '"' {
		var v string
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return err
		}
		s = strings.TrimSpace(v)
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > math.MaxInt32 {
		*f = 0
		return nil
	}
	*f = flexInt(n)
	return nil
}

type rawItem struct {
	KeyID           flexString `json:"keyId"`
	Title           flexString `json:"title"`
	Article         flexString `json:"article"`
	ModuleType      flexString `json:"moduleType"`
	ModuleAge       flexString `json:"moduleAge"`
	Structure       flexString `json:"structure"`
	MinAmount       flexInt    `json:"minAmount"`
	MaxAmount       flexInt    `json:"maxAmount"`
	MinDuration     flexInt    `json:"minDuration"`
	MaxDuration     flexInt    `json:"maxDuration"`
	FreeLevel       flexString `json:"freeLevel"`
	ReleaseDate     flexString `json:"releaseDate"`
	URL             flexString `json:"url"`
	Opinion         flexString `json:"opinion"`
	ModuleVersion   flexString `json:"moduleVersion"`
	OccurrencePlace flexString `json:"occurrencePlace"`
	Original        flexString `json:"original"`
}

func (r rawItem) item() CnmodsItem {
	return CnmodsItem{
		KeyID:           string(r.KeyID),
		Title:           string(r.Title),
		Article:         string(r.Article),
		ModuleType:      string(r.ModuleType),
		ModuleAge:       string(r.ModuleAge),
		Structure:       string(r.Structure),
		MinAmount:       int(r.MinAmount),
		MaxAmount:       int(r.MaxAmount),
		MinDuration:     int(r.MinDuration),
		MaxDuration:     int(r.MaxDuration),
		FreeLevel:       string(r.FreeLevel),
		ReleaseDate:     string(r.ReleaseDate),
		URL:             string(r.URL),
		Opinion:         string(r.Opinion),
		ModuleVersion:   string(r.ModuleVersion),
		OccurrencePlace: string(r.OccurrencePlace),
		Original:        string(r.Original),
	}
}

// List 查询 cnmods 的模组列表（无需鉴权）。
func (c *Client) List(ctx context.Context, q ListQuery) (ListPage, error) {
	page := q.Page
	if page < 1 {
		page = 1
	}
	moduleType := strings.TrimSpace(q.ModuleType)
	if moduleType == "" {
		moduleType = "COC"
	}
	listURL := c.ListURL
	if listURL == "" {
		listURL = defaultListURL
	}
	u, err := url.Parse(listURL)
	if err != nil {
		return ListPage{}, fmt.Errorf("无效的列表地址: %w", err)
	}
	// NOTE: title 必须经 Encode 做 URL 编码，来源站对未编码的中文不可靠。
	query := u.Query()
	query.Set("page", strconv.Itoa(page))
	query.Set("moduleType", moduleType)
	if title := strings.TrimSpace(q.Title); title != "" {
		query.Set("title", title)
	}
	u.RawQuery = query.Encode()

	ctx, cancel := withDefaultTimeout(ctx, listTimeout)
	defer cancel()
	hdr := http.Header{"Accept": {"application/json, text/plain, */*"}}
	body, final, status, err := c.fetch(ctx, http.MethodGet, u.String(), nil, hdr, maxPageBytes)
	if err != nil {
		return ListPage{}, err
	}
	if err := checkStatus(status, final); err != nil {
		return ListPage{}, err
	}

	var env struct {
		Code    flexInt         `json:"code"`
		Msg     flexString      `json:"msg"`
		Message flexString      `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		log.Warn("cnmods list decode failed", "err", err, "body_head", head(body, 500))
		return ListPage{}, fmt.Errorf("解析 cnmods 列表失败: %w", err)
	}
	if env.Code != 1 {
		msg := string(env.Msg)
		if msg == "" {
			msg = string(env.Message)
		}
		return ListPage{}, fmt.Errorf("cnmods 返回 code=%d: %s", int(env.Code), msg)
	}
	var data struct {
		List          []rawItem `json:"list"`
		TotalElements flexInt   `json:"totalElements"`
		TotalPages    flexInt   `json:"totalPages"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		log.Warn("cnmods list data decode failed", "err", err, "body_head", head(body, 500))
		return ListPage{}, fmt.Errorf("解析 cnmods 列表失败: %w", err)
	}
	items := make([]CnmodsItem, 0, len(data.List))
	for _, r := range data.List {
		items = append(items, r.item())
	}
	return ListPage{
		Items:         items,
		Page:          page,
		TotalPages:    int(data.TotalPages),
		TotalElements: int(data.TotalElements),
	}, nil
}
