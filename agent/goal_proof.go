package agent

import (
	"strings"
	"unicode/utf8"
)

const minGoalProofReasonRunes = 20

var placeholderGoalProofReasons = map[string]bool{
	"placeholder": true,
	"todo":        true,
	"tbd":         true,
	"test":        true,
	"testing":     true,
	"ok":          true,
	"done":        true,
	"yes":         true,
	"测试":          true,
	"测试目标":        true,
	"目标已达成":       true,
	"已完成":         true,
	"达成":          true,
}

func goalProofReasonError(raw string) string {
	reason := strings.TrimSpace(raw)
	if utf8.RuneCountInString(reason) < minGoalProofReasonRunes {
		return "reason 必须具体说明证据如何满足目标的验收条件，至少 20 个字符；不接受 placeholder/测试目标等占位理由"
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(reason), " "))
	if placeholderGoalProofReasons[normalized] {
		return "reason 不能使用占位或测试文本"
	}
	return ""
}
