package server

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNormalizePublicIP(t *testing.T) {
	for raw, want := range map[string]string{
		"64.71.188.83":      "64.71.188.83",
		" 2001:db8::1 ":     "2001:db8::1",
		"not-an-ip":         "",
		"":                  "",
		"999.999.999.999":   "",
		"::ffff:192.0.2.10": "192.0.2.10",
	} {
		if got := normalizePublicIP(raw); got != want {
			t.Fatalf("normalizePublicIP(%q)=%q, want %q", raw, got, want)
		}
	}
}

func TestJoinIPLocationDeduplicates(t *testing.T) {
	if got := joinIPLocation(" 美国 ", "加利福尼亚州", "旧金山", "美国", ""); got != "美国 · 加利福尼亚州 · 旧金山" {
		t.Fatalf("location=%q", got)
	}
	if got := joinIPLocation("", " "); got != "未知" {
		t.Fatalf("empty location=%q", got)
	}
}

func TestProbePublicIPLive(t *testing.T) {
	if os.Getenv("ZEROX_LIVE_NETWORK_TEST") != "1" {
		t.Skip("set ZEROX_LIVE_NETWORK_TEST=1 to exercise public providers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	info, err := probePublicIP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if normalizePublicIP(info.IP) == "" || info.Location == "" || info.CheckedAt.IsZero() {
		t.Fatalf("incomplete public IP info: %#v", info)
	}
}
