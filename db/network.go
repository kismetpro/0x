package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// NetworkSample is one periodic internet-reachability probe. State is derived
// from the external checks (DNS/TCP/HTTP): online = at least two succeeded,
// degraded = one, offline = none. ICMP/gateway are informational.
type NetworkSample struct {
	TS            time.Time       `json:"ts"`
	State         string          `json:"state"`
	DNSOK         bool            `json:"dns_ok"`
	TCPOK         bool            `json:"tcp_ok"`
	HTTPOK        bool            `json:"http_ok"`
	ICMPOK        bool            `json:"icmp_ok"`
	GatewayOK     bool            `json:"gateway_ok"`
	LatencyMS     int             `json:"latency_ms"`
	PublicIP      string          `json:"public_ip"`
	IPLocation    string          `json:"ip_location"`
	IPCountryCode string          `json:"ip_country_code"`
	IPISP         string          `json:"ip_isp"`
	IPCheckedAt   *time.Time      `json:"ip_checked_at,omitempty"`
	Detail        json.RawMessage `json:"detail,omitempty"`
}

// InsertNetworkSample appends one probe. A nil TS lets PostgreSQL stamp now().
func (d *DB) InsertNetworkSample(ctx context.Context, s NetworkSample) error {
	detail := s.Detail
	if len(detail) == 0 || !json.Valid(detail) {
		detail = json.RawMessage(`{}`)
	}
	var ts *time.Time
	if !s.TS.IsZero() {
		t := s.TS
		ts = &t
	}
	_, err := d.ExecContext(ctx, `
INSERT INTO network_samples(ts,state,dns_ok,tcp_ok,http_ok,icmp_ok,gateway_ok,latency_ms,
                            public_ip,ip_location,ip_country_code,ip_isp,ip_checked_at,detail)
VALUES (COALESCE($1::timestamptz, now()), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		ts, s.State, s.DNSOK, s.TCPOK, s.HTTPOK, s.ICMPOK, s.GatewayOK, s.LatencyMS,
		s.PublicIP, s.IPLocation, s.IPCountryCode, s.IPISP, s.IPCheckedAt, string(detail))
	return err
}

// LatestNetworkSample returns the newest probe, or nil when none exist.
func (d *DB) LatestNetworkSample(ctx context.Context) (*NetworkSample, error) {
	row := d.QueryRowContext(ctx, `
SELECT ts,state,dns_ok,tcp_ok,http_ok,icmp_ok,gateway_ok,latency_ms,
       public_ip,ip_location,ip_country_code,ip_isp,ip_checked_at,detail
FROM network_samples WHERE ts <= now() ORDER BY ts DESC LIMIT 1`)
	var s NetworkSample
	var detail []byte
	var ipCheckedAt sql.NullTime
	if err := row.Scan(&s.TS, &s.State, &s.DNSOK, &s.TCPOK, &s.HTTPOK, &s.ICMPOK,
		&s.GatewayOK, &s.LatencyMS, &s.PublicIP, &s.IPLocation, &s.IPCountryCode,
		&s.IPISP, &ipCheckedAt, &detail); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if ipCheckedAt.Valid {
		checkedAt := ipCheckedAt.Time
		s.IPCheckedAt = &checkedAt
	}
	if json.Valid(detail) {
		s.Detail = detail
	}
	return &s, nil
}

// NetworkHistoryBucket is one minute of samples. A minute containing any offline
// sample is reported offline (conservative), then degraded, otherwise online.
type NetworkHistoryBucket struct {
	TS        time.Time `json:"ts"`
	State     string    `json:"state"`
	Samples   int       `json:"samples"`
	LatencyMS int       `json:"latency_ms"`
}

// NetworkHistory returns per-minute buckets for the last hours.
func (d *DB) NetworkHistory(ctx context.Context, hours int) ([]NetworkHistoryBucket, error) {
	if hours <= 0 {
		hours = 24
	}
	rows, err := d.QueryContext(ctx, `
SELECT date_trunc('minute', ts) AS bucket,
       CASE WHEN bool_or(state='offline') THEN 'offline'
            WHEN bool_or(state='degraded') THEN 'degraded'
            ELSE 'online' END AS state,
       count(*),
       COALESCE(round(avg(latency_ms)),0)::int
FROM network_samples
WHERE ts >= now() - ($1 * interval '1 hour') AND ts <= now()
GROUP BY 1 ORDER BY 1`, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkHistoryBucket{}
	for rows.Next() {
		var b NetworkHistoryBucket
		if err := rows.Scan(&b.TS, &b.State, &b.Samples, &b.LatencyMS); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// NetworkTransition is a state change (the first sample of a new state).
type NetworkTransition struct {
	TS    time.Time `json:"ts"`
	State string    `json:"state"`
}

// NetworkTransitions returns the first sample of every contiguous state run in
// the last hours, so callers can pair offline→online into outage intervals.
func (d *DB) NetworkTransitions(ctx context.Context, hours int) ([]NetworkTransition, error) {
	if hours <= 0 {
		hours = 24
	}
	rows, err := d.QueryContext(ctx, `
WITH samples AS (
    SELECT ts, state FROM network_samples
    WHERE ts >= now() - ($1 * interval '1 hour') AND ts <= now()
), marked AS (
    SELECT ts, state, lag(state) OVER (ORDER BY ts) AS prev FROM samples
)
SELECT ts, state FROM marked WHERE prev IS DISTINCT FROM state ORDER BY ts`, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkTransition{}
	for rows.Next() {
		var t NetworkTransition
		if err := rows.Scan(&t.TS, &t.State); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PruneNetworkSamples deletes samples older than the retention window.
func (d *DB) PruneNetworkSamples(ctx context.Context, retention time.Duration) (int64, error) {
	res, err := d.ExecContext(ctx, `DELETE FROM network_samples WHERE ts < now() - $1::interval`,
		retention.String())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
