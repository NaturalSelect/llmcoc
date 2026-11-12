package models

import "testing"

func TestNormalizeGameTime(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "1924-09-03T21:30", want: "1924-09-03T21:30"},
		{in: "1924-09-03 21:30", want: "1924-09-03T21:30"},
		{in: "1924-09-03T21:30:45", want: "1924-09-03T21:30"},
		{in: "  1924-09-03T08:05  ", want: "1924-09-03T08:05"},
		{in: "1924-09-03T21:30Z", wantErr: true},
		{in: "1924-09-03T21:30+08:00", wantErr: true},
		{in: "1924-09-03", wantErr: true},
		{in: "1924-13-03T21:30", wantErr: true},
		{in: "1924-02-30T21:30", wantErr: true},
		{in: "第1天 08:00", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, c := range cases {
		got, err := NormalizeGameTime(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeGameTime(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeGameTime(%q) error = %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeGameTime(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGameDayKey(t *testing.T) {
	if got := GameDayKey("1924-09-03T21:30"); got != 19240903 {
		t.Errorf("GameDayKey = %d, want 19240903", got)
	}
	if got := GameDayKey("1924-09-04T00:00"); got != 19240904 {
		t.Errorf("GameDayKey across midnight = %d, want 19240904", got)
	}
	for _, bad := range []string{"", "黄昏", "1924-09-03T21:30Z"} {
		if got := GameDayKey(bad); got != 0 {
			t.Errorf("GameDayKey(%q) = %d, want 0", bad, got)
		}
	}
}

func TestLegacyGameStartTime(t *testing.T) {
	cases := []struct {
		name    string
		setting string
		slot    int
		want    string
	}{
		{"日期加槽位", "1923年10月15日，阿卡姆的雨夜", 36, "1923-10-15T18:00"},
		{"半点槽位", "1923年10月15号清晨", 17, "1923-10-15T08:30"},
		{"零点槽位合法", "1923年10月15日", 0, "1923-10-15T00:00"},
		{"槽位越界按08:00", "1923年10月15日", 99, "1923-10-15T08:00"},
		{"负槽位按08:00", "1923年10月15日", -1, "1923-10-15T08:00"},
		{"取第一个日期", "1923年10月15日的案子牵出1890年1月2日的旧事", 20, "1923-10-15T10:00"},
		{"没有日期不编造", "阿卡姆的雨夜", 36, ""},
		{"非法日期不编造", "1923年2月30日", 36, ""},
	}
	for _, c := range cases {
		if got := LegacyGameStartTime(c.setting, c.slot); got != c.want {
			t.Errorf("%s: LegacyGameStartTime(%q, %d) = %q, want %q", c.name, c.setting, c.slot, got, c.want)
		}
	}
}
