package f35

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const whoisURL = "https://api.ipiz.net"

type whoisResponse struct {
	OrgName string `json:"org_name"`
	Country string `json:"country"`
	Status  string `json:"status"`
}

func doHTTPCheck(client *http.Client, targetURL string, timeout time.Duration, drainBody bool) (int64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("Connection", "close")

	startedAt := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()

	if drainBody {
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			return 0, false
		}
	}

	return time.Since(startedAt).Milliseconds(), true
}

func doUploadCheck(client *http.Client, targetURL string, timeout time.Duration, payload []byte) (int64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return 0, false
	}
	req.Header.Set("Connection", "close")
	req.Header.Set("Content-Type", "application/octet-stream")

	startedAt := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()

	return time.Since(startedAt).Milliseconds(), true
}

func lookupResolverInfo(client *http.Client, resolver string, timeout time.Duration) (int64, string, string, bool) {
	host, _, err := net.SplitHostPort(resolver)
	if err != nil {
		return 0, "unknown", "unknown", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, whoisURL+"/"+host, nil)
	if err != nil {
		return 0, "unknown", "unknown", false
	}
	req.Header.Set("Connection", "close")

	startedAt := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, "unknown", "unknown", false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "unknown", "unknown", false
	}

	var data whoisResponse
	if err := json.Unmarshal(body, &data); err != nil || strings.TrimSpace(data.Status) != "ok" {
		return 0, "unknown", "unknown", false
	}

	org := strings.TrimSpace(data.OrgName)
	if org == "" {
		org = "unknown"
	}
	country := strings.TrimSpace(data.Country)
	if country == "" {
		country = "unknown"
	}
	return time.Since(startedAt).Milliseconds(), org, country, true
}
