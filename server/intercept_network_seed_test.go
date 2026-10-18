package server

import (
	"strings"
	"testing"

	"github.com/kismetpro/0x/db"
	"github.com/kismetpro/0x/intercept"
)

// 端到端：新库启动时自动播种 v4 规则，且匹配引擎会在命令执行前拒绝事故命令。
func TestNetworkStormRulesSeededAndEnforced(t *testing.T) {
	dsn, _, dsnErr := db.DSN()
	if dsnErr != nil {
		t.Skipf("no database config (%v) - skipping", dsnErr)
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	rules, err := d.ListInterceptRules()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"[内置] 大网段批量扫描（/16 及更大）",
		"[内置] 批量 PTR 反向 DNS 扫描",
		"[内置] 高并发批量请求（≥16 并发）",
		"[内置] 高速率端口扫描（--min-rate ≥500 / -T5）",
		"[内置] 大范围 UDP 扫描",
	}
	byName := map[string]db.InterceptRule{}
	for _, r := range rules {
		byName[r.Name] = r
	}
	for _, name := range want {
		r, ok := byName[name]
		if !ok {
			t.Fatalf("missing seeded rule %q", name)
		}
		if !r.Enabled || r.Action != "deny" || r.MatchTarget != "tool_input" {
			t.Fatalf("rule %q not an enabled deny rule: %+v", name, r)
		}
	}
	ic := intercept.New(d)
	denied := []string{
		`{"command":"nohup python3 ptr_scan.py out.jsonl 115.156.0.0/16 > log.txt 2>&1 &"}`,
		`{"command":"nmap -Pn -sS -p- --open -T4 --min-rate 3000 222.194.190.69 -oA fullport"}`,
		`{"command":"nmap -Pn -sU --top-ports 200 --min-rate 1500 202.112.209.92"}`,
		`{"command":"cat dict.txt | xargs -P 64 -I{} dig +short {}.example.edu.cn A"}`,
		`{"content":"with ThreadPoolExecutor(max_workers=128) as pool:"}`,
	}
	for _, in := range denied {
		if dec, ok := ic.Match("Bash", []byte(in)); !ok || dec.Action != "deny" {
			t.Fatalf("incident command was not denied: %s (decision=%+v ok=%v)", in, dec, ok)
		} else if !strings.Contains(dec.Message, "不是禁止") || !strings.Contains(dec.Message, "重试") {
			// 拒绝理由会原样进入模型的 tool_result：必须说明这是限流而非封禁，
			// 并给出可调整后重试的方向，否则模型会放弃整条测试思路。
			t.Fatalf("deny message gives no safe retry path: %q", dec.Message)
		}
	}
	allowed := []string{
		`{"command":"nmap -sn 202.114.64.0/24"}`,
		`{"command":"nmap -sU -p123 202.114.64.16"}`,
		`{"command":"nmap --min-rate 200 -p80,443 202.114.64.16"}`,
	}
	for _, in := range allowed {
		if dec, ok := ic.Match("Bash", []byte(in)); ok && dec.Action == "deny" {
			t.Fatalf("normal command was denied by %q: %s", dec.Message, in)
		}
	}
	if !strings.Contains(byName[want[1]].Message, "反查") || !strings.Contains(byName[want[1]].Message, "dig -x") {
		t.Fatalf("deny message should explain the PTR risk: %q", byName[want[1]].Message)
	}
	if !strings.Contains(byName[want[3]].Message, "--max-rate 200") {
		t.Fatalf("rate-limit rule should suggest a safe retry parameter: %q", byName[want[3]].Message)
	}
}
