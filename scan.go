package f35

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

type scanStats struct {
	mu        sync.Mutex
	total     int
	processed int
	healthy   int
}

func Scan(cfg Config, hooks Hooks) error {
	runtime, err := prepareConfig(cfg)
	if err != nil {
		return err
	}

	return scanResolvers(&runtime, hooks)
}

func scanResolvers(runtime *runtimeConfig, hooks Hooks) error {
	total := len(runtime.parsedResolvers)
	jobs := make(chan parsedResolver, runtime.Workers*2)
	stats := newScanStats(total)

	var wg sync.WaitGroup
	for i := 0; i < runtime.Workers; i++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			worker(port, runtime, jobs, hooks, stats)
		}(runtime.StartPort + i)
	}

	for _, resolver := range runtime.parsedResolvers {
		jobs <- resolver
	}
	close(jobs)

	wg.Wait()
	return nil
}

func worker(port int, cfg *runtimeConfig, jobs <-chan parsedResolver, hooks Hooks, stats *scanStats) {
	proxyURL := &url.URL{
		Scheme: cfg.Proxy,
		Host:   net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
	}
	if cfg.ProxyUser != "" {
		if cfg.ProxyPass != "" {
			proxyURL.User = url.UserPassword(cfg.ProxyUser, cfg.ProxyPass)
		} else {
			proxyURL.User = url.User(cfg.ProxyUser)
		}
	}

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       0,
		Transport: &http.Transport{
			Proxy:                 http.ProxyURL(proxyURL),
			DisableKeepAlives:     true,
			DialContext:           (&net.Dialer{Timeout: 0, KeepAlive: 0}).DialContext,
			TLSHandshakeTimeout:   0,
			ResponseHeaderTimeout: 0,
			ExpectContinueTimeout: 0,
			IdleConnTimeout:       0,
		},
	}

	for resolver := range jobs {
		result, success := tryResolverWithRetries(resolver, port, cfg, client)
		progress := stats.Record(success)
		if hooks.OnProgress != nil {
			hooks.OnProgress(progress)
		}
		if success && hooks.OnResult != nil {
			hooks.OnResult(result)
		}
	}
}

func tryResolverWithRetries(resolver parsedResolver, port int, cfg *runtimeConfig, client *http.Client) (Result, bool) {
	for attempt := 0; attempt <= cfg.Retries; attempt++ {
		result, success := tryResolver(resolver, port, cfg, client)
		if success {
			return result, true
		}
	}
	return Result{}, false
}

func newScanStats(total int) *scanStats {
	return &scanStats{
		total: total,
	}
}

func (s *scanStats) Record(success bool) Progress {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.processed++
	if success {
		s.healthy++
	}

	return Progress{
		Total:     s.total,
		Processed: s.processed,
		Healthy:   s.healthy,
		Failed:    s.processed - s.healthy,
	}
}

func tryResolver(resolver parsedResolver, port int, cfg *runtimeConfig, client *http.Client) (Result, bool) {
	cmd := exec.Command(cfg.ClientPath, buildEngineArgs(cfg, resolver.addr, port)...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return Result{}, false
	}

	cmdDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(cmdDone)
	}()

	waited := false
	defer func() {
		if waited {
			return
		}
		_ = cmd.Process.Kill()
		<-cmdDone
	}()

	if cfg.TunnelWait > 0 {
		timer := time.NewTimer(cfg.TunnelWait)
		select {
		case <-timer.C:
		case <-cmdDone:
			waited = true
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return Result{}, false
		}
	}

	result := Result{
		Resolver: resolver.addr,
		Download: "off",
		Upload:   "off",
		Whois:    "off",
		Probe:    "off",
	}

	bestPriority := 0

	if cfg.Download {
		result.Download = "fail"
		latency, ok := runCheckUntilSuccess(cfg.DownloadTimeout, cmdDone, func(timeout time.Duration) (int64, bool) {
			return doHTTPCheck(client, http.MethodGet, cfg.DownloadURL, timeout, true)
		})
		if ok {
			result.Download = "ok"
			result.LatencyMS = latency
			bestPriority = 4
		}
	}

	if cfg.Upload {
		result.Upload = "fail"
		latency, ok := runCheckUntilSuccess(cfg.UploadTimeout, cmdDone, func(timeout time.Duration) (int64, bool) {
			return doUploadCheck(client, cfg.UploadURL, timeout, cfg.uploadPayload)
		})
		if ok {
			result.Upload = "ok"
			if bestPriority < 3 {
				result.LatencyMS = latency
				bestPriority = 3
			}
		}
	}

	if cfg.Whois {
		result.Whois = "fail"
		latency, org, country, ok := runWhoisUntilSuccess(cfg.WhoisTimeout, cmdDone, func(timeout time.Duration) (int64, string, string, bool) {
			return lookupResolverInfo(client, resolver.ip.String(), timeout)
		})
		if ok {
			result.Whois = "ok"
			result.Org = org
			result.Country = country
			if bestPriority < 2 {
				result.LatencyMS = latency
				bestPriority = 2
			}
		}
	}

	if cfg.Probe {
		result.Probe = "fail"
		latency, ok := runCheckUntilSuccess(cfg.ProbeTimeout, cmdDone, func(timeout time.Duration) (int64, bool) {
			return doHTTPCheck(client, cfg.ProbeHTTPMethod, cfg.ProbeURL, timeout, false)
		})
		if ok {
			result.Probe = "ok"
			if bestPriority < 1 {
				result.LatencyMS = latency
				bestPriority = 1
			}
		}
	}

	if bestPriority == 0 {
		return Result{}, false
	}

	return result, true
}

func runCheckUntilSuccess(timeout time.Duration, cmdDone <-chan struct{}, check func(time.Duration) (int64, bool)) (int64, bool) {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, false
		}

		latency, ok := check(remaining)
		if ok {
			return latency, true
		}

		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-cmdDone:
			if !timer.Stop() {
				<-timer.C
			}
			return 0, false
		case <-timer.C:
		}
	}
}

func runWhoisUntilSuccess(timeout time.Duration, cmdDone <-chan struct{}, check func(time.Duration) (int64, string, string, bool)) (int64, string, string, bool) {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, "unknown", "unknown", false
		}

		latency, org, country, ok := check(remaining)
		if ok {
			return latency, org, country, true
		}

		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-cmdDone:
			if !timer.Stop() {
				<-timer.C
			}
			return 0, "unknown", "unknown", false
		case <-timer.C:
		}
	}
}
