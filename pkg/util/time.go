package util

import "time"

// FormatUnixMilliRFC3339 将毫秒时间戳格式化为 RFC3339（UTC）
func FormatUnixMilliRFC3339(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.Unix(0, ms*int64(time.Millisecond)).UTC().Format(time.RFC3339)
}

// TimeToUnixMilli 将 time.Time 转为毫秒时间戳
func TimeToUnixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
