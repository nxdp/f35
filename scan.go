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
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			DisableKeepAlives: true,
		},
	}

	for resolver := range jobs {
		result, success := tryResolver(resolver, port, cfg, client)
		progress := stats.Record(success)
		if hooks.OnProgress != nil {
			hooks.OnProgress(progress)
		}
		if success && hooks.OnResult != nil {
			hooks.OnResult(result)
		}
	}
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
	args, err := buildEngineArgs(cfg, resolver.addr, port)
	if err != nil {
		return Result{}, false
	}

	cmd := exec.Command(cfg.ClientPath, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return Result{}, false
	}

	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	time.Sleep(cfg.TunnelWait)

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
		latency, ok := doHTTPCheck(client, cfg.DownloadURL, cfg.DownloadTimeout, true)
		if ok {
			result.Download = "ok"
			result.LatencyMS = latency
			bestPriority = 4
		}
	}

	if cfg.Upload {
		result.Upload = "fail"
		latency, ok := doUploadCheck(client, cfg.UploadURL, cfg.UploadTimeout, cfg.uploadPayload)
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
		latency, org, country, ok := lookupResolverInfo(client, resolver.ip.String(), cfg.WhoisTimeout)
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
		latency, ok := doHTTPCheck(client, cfg.ProbeURL, cfg.ProbeTimeout, false)
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
