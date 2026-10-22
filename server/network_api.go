package server

import (
	"net/http"
	"time"

	"github.com/kismetpro/0x/db"
)

type networkOutageDTO struct {
	DownAt  time.Time  `json:"down_at"`
	UpAt    *time.Time `json:"up_at,omitempty"`
	Seconds int64      `json:"seconds"`
	Ongoing bool       `json:"ongoing"`
}

// buildNetworkOutages pairs offline runs with the next reachable state (online
// or degraded) into outage intervals. A run still open at the end is marked
// ongoing=true.
func buildNetworkOutages(transitions []db.NetworkTransition, now time.Time) []networkOutageDTO {
	out := []networkOutageDTO{}
	var down *time.Time
	for _, t := range transitions {
		if t.State == "offline" {
			if down == nil {
				d := t.TS
				down = &d
			}
			continue
		}
		if down != nil {
			up := t.TS
			out = append(out, networkOutageDTO{
				DownAt: *down, UpAt: &up,
				Seconds: int64(up.Sub(*down).Seconds()),
			})
			down = nil
		}
	}
	if down != nil {
		secs := int64(now.Sub(*down).Seconds())
		if secs < 0 {
			secs = 0
		}
		out = append(out, networkOutageDTO{DownAt: *down, Seconds: secs, Ongoing: true})
	}
	return out
}

func tailOutages(in []networkOutageDTO, n int) []networkOutageDTO {
	if len(in) <= n {
		return in
	}
	return in[len(in)-n:]
}

// networkStatus returns the newest probe plus the most recent outage summary.
func (s *Server) networkStatus(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeJSON(w, 200, map[string]any{
			"sample": nil, "outages": []networkOutageDTO{}, "monitor": networkMonitorInfo(),
		})
		return
	}
	ctx := r.Context()
	sample, err := pg.LatestNetworkSample(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	transitions, err := pg.NetworkTransitions(ctx, 7*24)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	outages := buildNetworkOutages(transitions, time.Now())
	var since *time.Time
	if sample != nil {
		for i := len(transitions) - 1; i >= 0; i-- {
			if !transitions[i].TS.After(sample.TS) {
				t := transitions[i].TS
				since = &t
				break
			}
		}
	}
	var last *networkOutageDTO
	if len(outages) > 0 {
		last = &outages[len(outages)-1]
	}
	writeJSON(w, 200, map[string]any{
		"sample":      sample,
		"since":       since,
		"outages":     tailOutages(outages, 5),
		"last_outage": last,
		"monitor":     networkMonitorInfo(),
	})
}

// networkHistory returns per-minute buckets plus outage intervals for the chart.
func (s *Server) networkHistory(w http.ResponseWriter, r *http.Request) {
	pg := s.m.PG()
	if pg == nil {
		writeJSON(w, 200, map[string]any{
			"buckets": []db.NetworkHistoryBucket{}, "outages": []networkOutageDTO{},
			"hours": 24, "monitor": networkMonitorInfo(),
		})
		return
	}
	hours := atoiDefault(r.URL.Query().Get("hours"), 24)
	if hours < 1 {
		hours = 1
	}
	if hours > 168 {
		hours = 168
	}
	ctx := r.Context()
	buckets, err := pg.NetworkHistory(ctx, hours)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	transitions, err := pg.NetworkTransitions(ctx, hours)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"buckets": buckets,
		"outages": buildNetworkOutages(transitions, time.Now()),
		"hours":   hours,
		"monitor": networkMonitorInfo(),
	})
}

func networkMonitorInfo() map[string]any {
	return map[string]any{
		"interval_seconds":   int(networkMonitorInterval.Seconds()),
		"retention_days":     int(networkRetention.Hours() / 24),
		"ip_refresh_seconds": int(networkIPRefresh.Seconds()),
	}
}
