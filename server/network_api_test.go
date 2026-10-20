package server

import (
	"testing"
	"time"

	"github.com/kismetpro/0x/db"
)

func TestBuildNetworkOutagesPairsRecoveryTransitions(t *testing.T) {
	base := time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)
	transitions := []db.NetworkTransition{
		{TS: base, State: "online"},
		{TS: base.Add(10 * time.Second), State: "offline"},
		{TS: base.Add(40 * time.Second), State: "degraded"},
		{TS: base.Add(70 * time.Second), State: "offline"},
		{TS: base.Add(2 * time.Minute), State: "online"},
	}

	out := buildNetworkOutages(transitions, base.Add(3*time.Minute))
	if len(out) != 2 {
		t.Fatalf("outages=%d, want 2: %#v", len(out), out)
	}
	if out[0].DownAt != base.Add(10*time.Second) || out[0].UpAt == nil ||
		*out[0].UpAt != base.Add(40*time.Second) || out[0].Seconds != 30 || out[0].Ongoing {
		t.Fatalf("first outage mismatch: %#v", out[0])
	}
	if out[1].DownAt != base.Add(70*time.Second) || out[1].UpAt == nil ||
		*out[1].UpAt != base.Add(2*time.Minute) || out[1].Seconds != 50 || out[1].Ongoing {
		t.Fatalf("second outage mismatch: %#v", out[1])
	}
}

func TestBuildNetworkOutagesKeepsOpenOutage(t *testing.T) {
	down := time.Date(2026, 10, 20, 5, 10, 0, 0, time.UTC)
	now := down.Add(95 * time.Second)
	out := buildNetworkOutages([]db.NetworkTransition{
		{TS: down.Add(-time.Minute), State: "online"},
		{TS: down, State: "offline"},
	}, now)
	if len(out) != 1 {
		t.Fatalf("outages=%d, want 1: %#v", len(out), out)
	}
	if !out[0].Ongoing || out[0].UpAt != nil || out[0].Seconds != 95 || out[0].DownAt != down {
		t.Fatalf("open outage mismatch: %#v", out[0])
	}
}

func TestTailOutagesReturnsNewestWindow(t *testing.T) {
	base := time.Date(2026, 10, 20, 6, 0, 0, 0, time.UTC)
	out := []networkOutageDTO{
		{DownAt: base},
		{DownAt: base.Add(time.Minute)},
		{DownAt: base.Add(2 * time.Minute)},
	}
	tail := tailOutages(out, 2)
	if len(tail) != 2 || tail[0].DownAt != out[1].DownAt || tail[1].DownAt != out[2].DownAt {
		t.Fatalf("tail mismatch: %#v", tail)
	}
}
