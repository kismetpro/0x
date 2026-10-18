package guard

import (
	"strings"
	"testing"
)

// 拦截消息会以 tool_result(is_error=true) 原样回给模型，因此必须同时说明
// 「这是平台限流、不是目标防御」和「给出替代参数时可以调整后重试」，
// 否则模型会把限速误读成整条测试思路被封死，直接放弃找漏洞。
func TestSystemBlockMessageKeepsModelAbleToAdjust(t *testing.T) {
	msg := systemBlockMessage("速率限流（不是禁止扫描）：请改用 --max-rate 200 分批重试")
	for _, want := range []string{
		"不是目标侧的 WAF/限流/拒绝",
		"原因：速率限流",
		"--max-rate 200",
		"调整后重试同一测试思路",
		"不要原样重复",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("block message missing %q: %s", want, msg)
		}
	}
	// 破坏性规则没有安全替代动作时，只应告诉模型不要重复，不能暗示可以换参数绕过。
	destructive := systemBlockMessage("破坏性命令被拒绝（rm -rf / / mkfs）")
	if !strings.Contains(destructive, "没有给出替代做法时，不要原样重复") {
		t.Errorf("destructive message lost the no-repeat guidance: %s", destructive)
	}
}
