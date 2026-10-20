package db

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestNetworkHistoryAndTransitions(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	ctx := context.Background()

	base := time.Now().UTC().Add(-5 * time.Hour).Truncate(time.Minute)
	samples := []NetworkSample{
		{TS: base.Add(5 * time.Second), State: "online", DNSOK: true, TCPOK: true, HTTPOK: true},
		{TS: base.Add(20 * time.Second), State: "offline", Detail: json.RawMessage(`{"dns":{"ok":false}}`)},
		{TS: base.Add(time.Minute + 10*time.Second), State: "degraded", DNSOK: true},
	}
	for _, sample := range samples {
		if err := d.InsertNetworkSample(ctx, sample); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM network_samples WHERE ts >= $1 AND ts <= $2`,
			base.Add(-time.Second), base.Add(2*time.Minute))
	})

	history, err := d.NetworkHistory(ctx, 24)
	if err != nil {
		t.Fatal(err)
	}
	got := map[time.Time]string{}
	for _, bucket := range history {
		got[bucket.TS.UTC()] = bucket.State
	}
	if got[base] != "offline" {
		t.Fatalf("offline must dominate its minute: %v", got[base])
	}
	if got[base.Add(time.Minute)] != "degraded" {
		t.Fatalf("degraded minute mismatch: %v", got[base.Add(time.Minute)])
	}

	transitions, err := d.NetworkTransitions(ctx, 24)
	if err != nil {
		t.Fatal(err)
	}
	want := map[time.Time]string{
		base.Add(20 * time.Second):             "offline",
		base.Add(time.Minute + 10*time.Second): "degraded",
	}
	for _, transition := range transitions {
		if state, ok := want[transition.TS.UTC()]; ok && transition.State != state {
			t.Fatalf("transition at %s = %s, want %s", transition.TS, transition.State, state)
		}
	}
	for ts, state := range want {
		found := false
		for _, transition := range transitions {
			if transition.TS.UTC().Equal(ts) && transition.State == state {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing transition %s %s", state, ts)
		}
	}
}

func TestUsageHourlyBucketsByUTCHour(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	if err := d.EnsureLLMUsageTable(); err != nil {
		t.Fatal(err)
	}

	profile := fmt.Sprintf("hourly-test-%d", time.Now().UnixNano())
	hour := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	for _, row := range []struct {
		ts     time.Time
		input  int
		output int
		cache  int
	}{
		{hour.Add(5 * time.Minute), 100, 10, 40},
		{hour.Add(35 * time.Minute), 200, 20, 80},
	} {
		if _, err := d.Exec(`
INSERT INTO llm_usage(ts,profile_name,input_tokens,output_tokens,cache_read)
VALUES ($1,$2,$3,$4,$5)`, row.ts, profile, row.input, row.output, row.cache); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM llm_usage WHERE profile_name=$1`, profile) })

	rows, err := d.UsageHourly(48)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ProfileName != profile {
			continue
		}
		if !row.Hour.UTC().Equal(hour) {
			t.Fatalf("hour=%s, want %s", row.Hour, hour)
		}
		if row.InputTokens != 300 || row.OutputTokens != 30 || row.CacheReadTokens != 120 {
			t.Fatalf("hourly aggregate mismatch: %#v", row)
		}
		return
	}
	t.Fatal("hourly aggregate not returned")
}
