package utils

import (
	"strings"
	"unicode/utf8"
)

// MaskTelephone 对手机号进行脱敏处理
// 示例: 13800138000 -> 138****8000
func MaskTelephone(telephone string) string {
	if len(telephone) < 7 {
		return telephone
	}
	return telephone[:3] + "****" + telephone[len(telephone)-4:]
}

// MaskEmail 对邮箱进行脱敏处理
// 示例: example@gmail.com -> e*****e@gmail.com
func MaskEmail(email string) string {
	if email == "" {
		return ""
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return email
	}
	username := parts[0]
	if utf8.RuneCountInString(username) <= 2 {
		return email
	}
	return string(username[0]) + "*****" + string(username[len(username)-1]) + "@" + parts[1]
}

// MaskUUID 对UUID进行脱敏处理
// 示例: 550e8400-e29b-41d4-a716-446655440000 -> 550e****-****-****-****-****440000
func MaskUUID(uuid string) string {
	if len(uuid) < 8 {
		return uuid
	}
	return uuid[:4] + "****-" + uuid[9:13] + "-****-" + uuid[19:23] + "-****-" + uuid[len(uuid)-6:]
}

// MaskPassword 对密码进行脱敏（只显示长度）
// 示例: password123 -> *********(10)
func MaskPassword(password string) string {
	if password == "" {
		return ""
	}
	return "*" + strings.Repeat("*", len(password)-1) + "(" + string(rune('0'+len(password)%10)) + ")"
}
