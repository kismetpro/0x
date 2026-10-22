package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kismetpro/0x/db"
)

// 网络实时监测：每 10 秒做一次轻量探测（DNS / 公共 TCP / HTTP / ICMP / 网关），
// 写入 network_samples。断网后可以按采样点精确回看何时断、何时恢复。
//
// 探测刻意保持「很小」：每个检查独立 3 秒超时并发执行，单轮最多几个连接，
// 不会反过来给家用网关制造压力。外部检查（DNS/TCP/HTTP）决定整体状态：
// ≥2 项通过 = online，1 项 = degraded，0 项 = offline。
const (
	networkMonitorInterval = 10 * time.Second
	networkProbeTimeout    = 3 * time.Second
	networkRetention       = 30 * 24 * time.Hour
)

type networkMonitor struct {
	pg *db.DB
}

var networkHTTPClient = &http.Client{
	Timeout: networkProbeTimeout,
	Transport: &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     30 * time.Second,
	},
}

func newNetworkMonitor(pg *db.DB) *networkMonitor {
	return &networkMonitor{pg: pg}
}

// Run samples until ctx is cancelled. Set ZEROX_NETWORK_MONITOR=off to disable.
func (m *networkMonitor) Run(ctx context.Context) {
	if m.pg == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ZEROX_NETWORK_MONITOR")), "off") {
		log.Printf("[netmon] disabled by ZEROX_NETWORK_MONITOR=off")
		return
	}
	log.Printf("[netmon] starting: interval=%s retention=%s", networkMonitorInterval, networkRetention)
	ticker := time.NewTicker(networkMonitorInterval)
	defer ticker.Stop()

	lastState := ""
	lastPrune := time.Time{}
	lastIPAttempt := time.Time{}
	lastIPSuccess := time.Time{}
	ipInfo := publicIPInfo{}
	for {
		sample := probeNetwork(ctx)
		if sample.State == "offline" {
			ipInfo = publicIPInfo{}
			lastIPAttempt = time.Time{}
			lastIPSuccess = time.Time{}
		} else {
			ipDue := false
			if ipInfo.IP == "" {
				ipDue = lastIPAttempt.IsZero() || time.Since(lastIPAttempt) >= time.Minute
			} else {
				ipDue = time.Since(lastIPSuccess) >= networkIPRefresh
			}
			if ipDue {
				lastIPAttempt = time.Now()
				next, err := probePublicIP(ctx)
				if err != nil {
					if ctx.Err() == nil {
						log.Printf("[netmon] public IP lookup: %v", err)
					}
				} else {
					ipInfo = next
					lastIPSuccess = time.Now()
					log.Printf("[netmon] public IP %s · %s · %s", ipInfo.IP, ipInfo.Location, orDash(ipInfo.ISP))
				}
			}
		}
		sample.PublicIP = ipInfo.IP
		sample.IPLocation = ipInfo.Location
		sample.IPCountryCode = ipInfo.CountryCode
		sample.IPISP = ipInfo.ISP
		if !ipInfo.CheckedAt.IsZero() {
			checkedAt := ipInfo.CheckedAt
			sample.IPCheckedAt = &checkedAt
		}
		if err := m.pg.InsertNetworkSample(ctx, sample); err != nil {
			if ctx.Err() == nil {
				log.Printf("[netmon] insert sample: %v", err)
			}
		} else if sample.State != lastState {
			log.Printf("[netmon] state %s → %s (dns=%v tcp=%v http=%v icmp=%v gw=%v latency=%dms ip=%s)",
				orDash(lastState), sample.State, sample.DNSOK, sample.TCPOK, sample.HTTPOK,
				sample.ICMPOK, sample.GatewayOK, sample.LatencyMS, orDash(sample.PublicIP))
			lastState = sample.State
		}
		if time.Since(lastPrune) >= time.Hour {
			lastPrune = time.Now()
			if n, err := m.pg.PruneNetworkSamples(ctx, networkRetention); err != nil {
				if ctx.Err() == nil {
					log.Printf("[netmon] prune: %v", err)
				}
			} else if n > 0 {
				log.Printf("[netmon] pruned %d samples older than %s", n, networkRetention)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

type probeResult struct {
	OK  bool   `json:"ok"`
	MS  int    `json:"ms"`
	Err string `json:"err,omitempty"`
}

// probeNetwork runs all checks concurrently and builds one sample.
func probeNetwork(parent context.Context) db.NetworkSample {
	ctx, cancel := context.WithTimeout(parent, networkProbeTimeout+time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var dnsR, tcpR, httpR, icmpR, gwR probeResult
	wg.Add(5)
	go func() { defer wg.Done(); dnsR = timedProbe(ctx, probeDNS) }()
	go func() { defer wg.Done(); tcpR = timedProbe(ctx, probeTCP) }()
	go func() { defer wg.Done(); httpR = timedProbe(ctx, probeHTTP) }()
	go func() { defer wg.Done(); icmpR = timedProbe(ctx, probeICMP) }()
	go func() { defer wg.Done(); gwR = timedProbe(ctx, probeGateway) }()
	wg.Wait()

	okCount := boolInt(dnsR.OK) + boolInt(tcpR.OK) + boolInt(httpR.OK)
	state := "online"
	switch {
	case okCount == 0:
		state = "offline"
	case okCount == 1:
		state = "degraded"
	}
	latency := 0
	for _, r := range []probeResult{dnsR, tcpR, httpR} {
		if r.OK && r.MS > latency {
			latency = r.MS
		}
	}
	detail, _ := json.Marshal(map[string]any{
		"dns": dnsR, "tcp": tcpR, "http": httpR, "icmp": icmpR, "gateway": gwR,
		"external_ok": okCount,
	})
	return db.NetworkSample{
		TS: time.Now().UTC(), State: state,
		DNSOK: dnsR.OK, TCPOK: tcpR.OK, HTTPOK: httpR.OK, ICMPOK: icmpR.OK,
		GatewayOK: gwR.OK, LatencyMS: latency, Detail: detail,
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// timedProbe enforces a per-check deadline and records wall time + error text.
func timedProbe(parent context.Context, fn func(context.Context) error) probeResult {
	ctx, cancel := context.WithTimeout(parent, networkProbeTimeout)
	defer cancel()
	start := time.Now()
	err := fn(ctx)
	ms := int(time.Since(start).Milliseconds())
	res := probeResult{OK: err == nil, MS: ms}
	if err != nil {
		res.Err = truncateErr(err)
	}
	return res
}

func truncateErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// probeDNS resolves well-known public names through the system resolver, which is
// the same path the agents use (Docker resolver → host → router/ONT).
func probeDNS(ctx context.Context) error {
	var lastErr error
	for _, host := range []string{"www.baidu.com", "www.qq.com"} {
		if _, err := net.DefaultResolver.LookupHost(ctx, host); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no DNS target")
	}
	return lastErr
}

// probeTCP checks direct outbound TCP to two widely reachable public resolvers.
func probeTCP(ctx context.Context) error {
	var lastErr error
	for _, addr := range []string{"223.5.5.5:443", "1.1.1.1:443"} {
		d := net.Dialer{}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return lastErr
}

// probeHTTP does one small HTTPS request. A clean transport (no proxy) measures
// the direct internet path; 5xx counts as a failure, 4xx still proves reachability.
func probeHTTP(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.baidu.com/", nil)
	if err != nil {
		return err
	}
	resp, err := networkHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 500 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// probeICMP pings a public address. Some networks drop ICMP; the result is
// informational and does not affect the derived state.
func probeICMP(ctx context.Context) error {
	bin, err := exec.LookPath("ping")
	if err != nil {
		return errors.New("ping not installed")
	}
	cmd := exec.CommandContext(ctx, bin, "-c", "1", "-W", "1", "223.5.5.5")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// probeGateway pings the container's default gateway (Docker bridge). It tells
// us whether the local link to the host is alive even when the internet is not.
func probeGateway(ctx context.Context) error {
	gw := defaultGateway()
	if gw == "" {
		return errors.New("default gateway not found")
	}
	bin, err := exec.LookPath("ping")
	if err != nil {
		return errors.New("ping not installed")
	}
	cmd := exec.CommandContext(ctx, bin, "-c", "1", "-W", "1", gw)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gateway %s: %v: %s", gw, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// defaultGateway parses /proc/net/route (Linux) for the IPv4 default route.
func defaultGateway() string {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		v, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil {
			continue
		}
		return net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)).String()
	}
	return ""
}
