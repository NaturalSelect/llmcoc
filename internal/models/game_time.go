// NOTE: game_time.go 定义剧中时间的统一格式：RFC 3339 的本地时间(无时区)，存成 YYYY-MM-DDTHH:MM。
// 剧中时间是虚构世界里的钟点，没有时区概念，所以带 Z 或偏移的输入一律拒绝。
package models

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const GameTimeLayout = "2006-01-02T15:04"

var gameTimeInputLayouts = []string{
	GameTimeLayout,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04",
	"2006-01-02 15:04:05",
}

// NOTE: 与 agent 包的 settingDateRe 保持同一写法：匹配 setting 里的"1923年10月15日/号"。
var legacySettingDateRe = regexp.MustCompile(`(\d{3,4})年(\d{1,2})月(\d{1,2})[日号]`)

// legacyDefaultSlot 是旧版 game_start_slot 越界时的回退槽位(08:00)。
const legacyDefaultSlot = 16

// ParseGameTime 接受 T 或空格分隔、可带秒的本地时间；带时区的输入会解析失败。
func ParseGameTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range gameTimeInputLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("时间 %q 不是 YYYY-MM-DDTHH:MM 格式的本地时间(不带时区)", s)
}

// NormalizeGameTime 解析后按 GameTimeLayout 输出，去掉秒。
func NormalizeGameTime(s string) (string, error) {
	t, err := ParseGameTime(s)
	if err != nil {
		return "", err
	}
	return t.Format(GameTimeLayout), nil
}

// GameDayKey 返回 YYYYMMDD 形式的日期键，空串或非法输入返回 0。
func GameDayKey(s string) int {
	t, err := ParseGameTime(s)
	if err != nil {
		return 0
	}
	return t.Year()*10000 + int(t.Month())*100 + t.Day()
}

// LegacyGameStartTime 用 setting 里的年月日和旧的 0-47 槽位(每槽30分钟)拼出开局时间。
// setting 里没有合法日期时返回空串，不编造日期。
func LegacyGameStartTime(setting string, slot int) string {
	m := legacySettingDateRe.FindStringSubmatch(setting)
	if m == nil {
		return ""
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	if slot < 0 || slot > 47 {
		slot = legacyDefaultSlot
	}
	t := time.Date(year, time.Month(month), day, slot/2, (slot%2)*30, 0, 0, time.UTC)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return ""
	}
	return t.Format(GameTimeLayout)
}
