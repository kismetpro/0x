package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const networkIPRefresh = 5 * time.Minute

type publicIPInfo struct {
	IP          string
	Location    string
	CountryCode string
	ISP         string
	CheckedAt   time.Time
}

// probePublicIP uses two geolocation providers and finally a plain IP echo.
// Providers are only consulted when the monitor's cache expires or the network
// recovers, never on every ten-second reachability sample.
func probePublicIP(parent context.Context) (publicIPInfo, error) {
	ctx, cancel := context.WithTimeout(parent, networkProbeTimeout*2+time.Second)
	defer cancel()

	var errs []string
	if info, err := probeIPWho(ctx); err == nil {
		return info, nil
	} else {
		errs = append(errs, "ipwho.is: "+truncateErr(err))
	}
	if info, err := probeIPAPI(ctx); err == nil {
		return info, nil
	} else {
		errs = append(errs, "ipapi.co: "+truncateErr(err))
	}
	if info, err := probeIPify(ctx); err == nil {
		return info, nil
	} else {
		errs = append(errs, "ipify.org: "+truncateErr(err))
	}
	return publicIPInfo{}, errors.New(strings.Join(errs, "; "))
}

func probeIPWho(ctx context.Context) (publicIPInfo, error) {
	var body struct {
		Success     bool   `json:"success"`
		Message     string `json:"message"`
		IP          string `json:"ip"`
		Country     string `json:"country"`
		CountryCode string `json:"country_code"`
		Region      string `json:"region"`
		City        string `json:"city"`
		Connection  struct {
			ISP string `json:"isp"`
		} `json:"connection"`
	}
	if err := getJSON(ctx, "https://ipwho.is/?lang=zh-CN", &body); err != nil {
		return publicIPInfo{}, err
	}
	if !body.Success {
		if body.Message == "" {
			body.Message = "lookup failed"
		}
		return publicIPInfo{}, errors.New(body.Message)
	}
	ip := normalizePublicIP(body.IP)
	if ip == "" {
		return publicIPInfo{}, errors.New("invalid public IP")
	}
	return publicIPInfo{
		IP: ip, Location: joinIPLocation(body.Country, body.Region, body.City),
		CountryCode: strings.ToUpper(strings.TrimSpace(body.CountryCode)),
		ISP:         strings.TrimSpace(body.Connection.ISP), CheckedAt: time.Now().UTC(),
	}, nil
}

func probeIPAPI(ctx context.Context) (publicIPInfo, error) {
	var body struct {
		IP          string `json:"ip"`
		Country     string `json:"country_name"`
		CountryCode string `json:"country_code"`
		Region      string `json:"region"`
		City        string `json:"city"`
		Org         string `json:"org"`
	}
	if err := getJSON(ctx, "https://ipapi.co/json/", &body); err != nil {
		return publicIPInfo{}, err
	}
	ip := normalizePublicIP(body.IP)
	if ip == "" {
		return publicIPInfo{}, errors.New("invalid public IP")
	}
	return publicIPInfo{
		IP: ip, Location: joinIPLocation(body.Country, body.Region, body.City),
		CountryCode: strings.ToUpper(strings.TrimSpace(body.CountryCode)),
		ISP:         strings.TrimSpace(body.Org), CheckedAt: time.Now().UTC(),
	}, nil
}

func probeIPify(ctx context.Context) (publicIPInfo, error) {
	var body struct {
		IP string `json:"ip"`
	}
	if err := getJSON(ctx, "https://api.ipify.org?format=json", &body); err != nil {
		return publicIPInfo{}, err
	}
	ip := normalizePublicIP(body.IP)
	if ip == "" {
		return publicIPInfo{}, errors.New("invalid public IP")
	}
	return publicIPInfo{IP: ip, Location: "未知", CheckedAt: time.Now().UTC()}, nil
}

func getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "0x-network-monitor/1.0")
	resp, err := networkHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return err
	}
	return nil
}

func normalizePublicIP(raw string) string {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.String()
}

func joinIPLocation(parts ...string) string {
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	if len(out) == 0 {
		return "未知"
	}
	return strings.Join(out, " · ")
}
