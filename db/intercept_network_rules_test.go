package db

import (
	"regexp"
	"testing"
)

// 这些命令来自真实任务历史：/16 批量 PTR 反查、xargs -P 64、128 线程池、
// --min-rate 1500/2000/3000 全端口扫描与大范围 UDP 扫描。它们必须在执行前被拦下。
func TestNetworkStormRulesMatchIncidentCommands(t *testing.T) {
	deny := []struct {
		pattern string
		command string
	}{
		{broadCIDRPattern, `nohup python3 ptr_scan.py ptr_115_156.jsonl 115.156.0.0/16 > log_115.txt 2>&1 &`},
		{broadCIDRPattern, `nmap -sn -n 10.0.0.0/8`},
		{bulkPTRPattern, `python3 ptr_scan.py out.jsonl 202.114.64.0-202.114.127.255`},
		{bulkPTRPattern, `ptr-scan 202.114.64.0/24`},
		{highParallelismPattern, `cat dict_clean.txt | xargs -P 64 -I{} sh -c 'dig +short {}.example.edu.cn A'`},
		{highParallelismPattern, `with ThreadPoolExecutor(max_workers=128) as pool:`},
		{highParallelismPattern, `pool = ThreadPoolExecutor(64, thread_name_prefix="dns")`},
		{highRateScanPattern, `cd /app/data/tasks/31/i1262 && nmap -Pn -T4 --min-rate 1500 --max-retries 2 -p 1-65535 --open 210.29.152.18`},
		{highRateScanPattern, `nohup nmap -sS -p- --open -T4 --min-rate 3000 222.194.190.69 -oA fullport &`},
		{highRateScanPattern, `cd /app/data/tasks/33/i836 && nohup nmap -Pn -n -T4 --min-rate 3000 --max-retries 1 -p- -oG tcp_all.gnmap -oN tcp_all.nmap $(cat ips.txt | tr '\n' ' ') > nmap_full.log 2>&1 &`},
		{highRateScanPattern, `nmap --min-rate=3000 -p- 10.0.0.5`},
		{highRateScanPattern, `nmap --max-rate=2000 -p- 10.0.0.5`},
		{highRateScanPattern, `masscan --rate=10000 -p1-65535 10.0.0.0/24`},
		{highRateScanPattern, `nmap -Pn -sT -p- --min-rate 2000 --open 210.47.0.168 -oN full_tcp.txt`},
		{highRateScanPattern, `nmap -Pn -sS -p- -T5 --open 10.0.0.5`},
		{highRateScanPattern, `masscan -p1-65535 --rate 10000 10.0.0.0/24`},
		{bulkUDPScanPattern, `nmap -Pn -sU --top-ports 200 --min-rate 1500 -T4 -oA udp_top200 202.112.209.92`},
		{bulkUDPScanPattern, `nmap -sU -p- --open 10.0.0.5`},
	}
	for _, tc := range deny {
		re := regexp.MustCompile(tc.pattern)
		if !re.MatchString(tc.command) {
			t.Errorf("expected deny match: %s", tc.command)
		}
	}

	// 正常粒度/低速操作不能被误伤，否则工具基本不可用。
	allow := []struct {
		pattern string
		command string
	}{
		{broadCIDRPattern, `nmap -sn 202.114.64.0/24`},
		{broadCIDRPattern, `nmap -sT -p 80,443 192.168.1.0/24`},
		{bulkPTRPattern, `dig -x 202.114.64.16 +short`},
		{bulkPTRPattern, `host 202.114.64.16`},
		{highParallelismPattern, `xargs -P 4 -I{} curl -s -o /dev/null {}.example.com`},
		{highParallelismPattern, `with ThreadPoolExecutor(max_workers=8) as pool:`},
		{highRateScanPattern, `nmap --min-rate 200 -p 80,443 10.0.0.5`},
		{highRateScanPattern, `nmap --min-rate=200 -p 80,443 10.0.0.5`},
		{highRateScanPattern, `nmap -T4 --max-rate 200 -p- 10.0.0.5`},
		{highRateScanPattern, `nmap -T4 --max-rate=200 -p- 10.0.0.5`},
		{highRateScanPattern, `masscan -p80 --rate 100 10.0.0.5`},
		{bulkUDPScanPattern, `nmap -sU -p123 10.0.0.5`},
		{bulkUDPScanPattern, `nmap -sU -p 53,123 --max-rate 50 10.0.0.5`},
	}
	for _, tc := range allow {
		re := regexp.MustCompile(tc.pattern)
		if re.MatchString(tc.command) {
			t.Errorf("unexpected deny match: %s", tc.command)
		}
	}
}
