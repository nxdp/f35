package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Engine          string
	ClientPath      string
	Domain          string
	ResolversFile   string
	TestURL         string
	DownloadURL     string
	UploadURL       string
	JSON            bool
	Quiet           bool
	Short           bool
	Probe           bool
	Download        bool
	Upload          bool
	Whois           bool
	Proxy           string
	ProxyUser       string
	ProxyPass       string
	Args            string
	ParsedArgs      []string
	UploadPayload   []byte
	Colorize        bool
	LogColorize     bool
	Workers         int
	Retries         int
	TunnelWait      int
	Timeout         int
	DownloadTimeout int
	UploadTimeout   int
	UploadBytes     int
	WhoisTimeout    int
	StartPort       int
}

type EngineSpec struct {
	DefaultBinary        string
	DefaultArgs          []string
	InsertArgsBeforeTail bool
}

var engineSpecs = map[string]EngineSpec{
	"dnstt": {
		DefaultBinary: "dnstt-client",
		DefaultArgs: []string{
			"-udp", "{resolver}",
			"{domain}",
			"{listen}",
		},
		InsertArgsBeforeTail: true,
	},
	"slipstream": {
		DefaultBinary: "slipstream-client",
		DefaultArgs: []string{
			"--tcp-listen-host", "{listen_host}",
			"--tcp-listen-port", "{listen_port}",
			"--resolver", "{resolver}",
			"--domain", "{domain}",
			"--keep-alive-interval", "200",
		},
	},
	"vaydns": {
		DefaultBinary: "vaydns-client",
		DefaultArgs: []string{
			"-domain", "{domain}",
			"-listen", "{listen}",
			"-udp", "{resolver}",
		},
	},
}

const whoisURL = "https://api.ipiz.net"
const defaultProgressUpdateInterval = time.Second

type whoisResponse struct {
	OrgName string `json:"org_name"`
	Country string `json:"country"`
	Status  string `json:"status"`
}

type Result struct {
	Resolver  string `json:"resolver"`
	LatencyMS int64  `json:"latency_ms"`
	Download  string `json:"download"`
	Upload    string `json:"upload"`
	Whois     string `json:"whois"`
	Probe     string `json:"probe"`
	Org       string `json:"org,omitempty"`
	Country   string `json:"country,omitempty"`
}

type statusPrinter struct {
	mu           sync.Mutex
	colorize     bool
	liveProgress bool
	progressLine string
	progressSeen bool
}

type scanStats struct {
	mu        sync.Mutex
	processed int64
	healthy   int64
}

func main() {
	if err := run(); err != nil {
		logf(fileIsTerminal(os.Stderr), "ERR", "%v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := parseFlags()

	if err := validateConfig(cfg); err != nil {
		flag.Usage()
		return err
	}

	resolvers, err := loadResolvers(cfg.ResolversFile)
	if err != nil {
		return err
	}

	startedAt := time.Now()
	status := newStatusPrinter(cfg)

	if !cfg.Quiet {
		status.Log("INFO", "starting | resolvers=%d | workers=%d | engine=%s", len(resolvers), cfg.Workers, cfg.Engine)
		status.Log("INFO", "config | checks=%s | wait=%dms | timeouts=%s", enabledChecks(cfg), cfg.TunnelWait, enabledTimeouts(cfg))
	}

	jobs := make(chan string, cfg.Workers*2)
	var wg sync.WaitGroup
	stats := &scanStats{}

	var stopProgress func()
	if !cfg.Quiet && status.liveProgress {
		stopProgress = startProgressReporter(status, len(resolvers), stats, startedAt)
	}

	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			worker(port, cfg, jobs, stats, status)
		}(cfg.StartPort + i)
	}

	for _, r := range resolvers {
		jobs <- r
	}
	close(jobs)

	wg.Wait()
	if stopProgress != nil {
		stopProgress()
	}
	if !cfg.Quiet {
		total, healthy := stats.Snapshot()
		level := "INFO"
		if healthy == 0 {
			level = "WARN"
		}
		status.Log(level, "completed | %d/%d | healthy=%d | failed=%d | elapsed=%s", total, len(resolvers), healthy, total-healthy, formatElapsed(time.Since(startedAt)))
	}
	return nil
}

func parseFlags() *Config {
	c := &Config{}

	flag.StringVar(&c.ResolversFile, "r", "", "Path to file containing resolvers (IP or IP:PORT per line)")
	flag.StringVar(&c.Engine, "e", "dnstt", fmt.Sprintf("Tunnel engine to use: %s", strings.Join(engineNames(), "|")))
	flag.StringVar(&c.ClientPath, "p", "", "Explicit path to client binary (optional)")
	flag.StringVar(&c.Domain, "d", "", "Tunnel domain (e.g., ns.example.com)")
	flag.StringVar(&c.Args, "a", "", "Extra engine CLI args; supports placeholders like {resolver}, {domain}, {listen}")
	flag.BoolVar(&c.JSON, "json", false, "Print one JSON object per result line")
	flag.BoolVar(&c.Quiet, "q", false, "Suppress startup and completion logs")
	flag.BoolVar(&c.Short, "short", false, "Print only IP:PORT and latency in plain text output")
	flag.StringVar(&c.TestURL, "u", "http://www.google.com/gen_204", "HTTP URL used for the probe request through the tunnel")
	flag.BoolVar(&c.Probe, "probe", true, "Run a quick connectivity probe through the tunnel")
	flag.BoolVar(&c.Download, "download", false, "Run a real download test through the tunnel")
	flag.StringVar(&c.DownloadURL, "download-url", "https://speed.cloudflare.com/__down?bytes=100000", "HTTP URL used for the download test")
	flag.BoolVar(&c.Upload, "upload", false, "Run a real upload test through the tunnel")
	flag.StringVar(&c.UploadURL, "upload-url", "https://speed.cloudflare.com/__up", "HTTP URL used for the upload test")
	flag.IntVar(&c.UploadBytes, "upload-bytes", 100000, "Number of bytes to send for the upload test")
	flag.BoolVar(&c.Whois, "whois", false, "Lookup resolver owner info and print organization and country")
	flag.StringVar(&c.Proxy, "x", "socks5h", "Protocol to use when sending request through the tunnel: http|https|socks5|socks5h")
	flag.StringVar(&c.ProxyUser, "U", "", "Proxy username (if the tunnel exit requires auth)")
	flag.StringVar(&c.ProxyPass, "P", "", "Proxy password (if the tunnel exit requires auth)")
	flag.IntVar(&c.Workers, "w", 20, "Number of concurrent scanning workers")
	flag.IntVar(&c.Retries, "R", 0, "Number of retries per resolver after the first failure")
	flag.IntVar(&c.TunnelWait, "s", 1000, "Time to wait (ms) for tunnel establishment before testing HTTP")
	flag.IntVar(&c.Timeout, "t", 15, "Probe request timeout in seconds")
	flag.IntVar(&c.DownloadTimeout, "download-timeout", 15, "Download request timeout in seconds")
	flag.IntVar(&c.UploadTimeout, "upload-timeout", 15, "Upload request timeout in seconds")
	flag.IntVar(&c.WhoisTimeout, "whois-timeout", 15, "WHOIS lookup timeout in seconds")
	flag.IntVar(&c.StartPort, "l", 40000, "Starting local port for tunnel listeners")

	flag.Parse()

	c.Engine = strings.ToLower(strings.TrimSpace(c.Engine))
	c.Proxy = strings.ToLower(strings.TrimSpace(c.Proxy))
	c.Colorize = !c.JSON && fileIsTerminal(os.Stdout)
	c.LogColorize = fileIsTerminal(os.Stderr)
	return c
}

func validateConfig(cfg *Config) error {
	if cfg.ResolversFile == "" || cfg.Domain == "" {
		return errors.New("-r and -d are required")
	}

	spec, ok := engineSpecs[cfg.Engine]
	if !ok {
		return fmt.Errorf("-e must be one of: %s", strings.Join(engineNames(), ", "))
	}

	if cfg.Args != "" {
		parsedArgs, err := splitCommandLine(cfg.Args)
		if err != nil {
			return fmt.Errorf("invalid -a: %w", err)
		}
		cfg.ParsedArgs = parsedArgs
	}

	switch cfg.Proxy {
	case "http", "https", "socks5", "socks5h":
	default:
		return errors.New("-x must be one of: http, https, socks5, socks5h")
	}

	if cfg.ProxyPass != "" && cfg.ProxyUser == "" {
		return errors.New("-P requires -U")
	}
	if !cfg.Probe && !cfg.Download && !cfg.Upload && !cfg.Whois {
		return errors.New("at least one of -probe, -download, -upload, or -whois must be enabled")
	}

	if cfg.Workers < 1 {
		return errors.New("-w must be >= 1")
	}
	if cfg.Retries < 0 {
		return errors.New("-R must be >= 0")
	}
	if cfg.Timeout < 1 {
		return errors.New("-t must be >= 1")
	}
	if cfg.DownloadTimeout < 1 {
		return errors.New("--download-timeout must be >= 1")
	}
	if cfg.UploadTimeout < 1 {
		return errors.New("--upload-timeout must be >= 1")
	}
	if cfg.UploadBytes < 1 {
		return errors.New("--upload-bytes must be >= 1")
	}
	if cfg.WhoisTimeout < 1 {
		return errors.New("--whois-timeout must be >= 1")
	}
	if cfg.TunnelWait < 0 {
		return errors.New("-s must be >= 0")
	}
	if cfg.StartPort < 1 || cfg.StartPort > 65535 {
		return errors.New("-l must be between 1 and 65535")
	}
	if cfg.StartPort+cfg.Workers-1 > 65535 {
		return errors.New("port range overflow (-l + -w exceeds 65535)")
	}

	if cfg.ClientPath == "" {
		path, err := exec.LookPath(spec.DefaultBinary)
		if err != nil {
			return fmt.Errorf("binary %s not found in PATH; use -p to specify path", spec.DefaultBinary)
		}
		cfg.ClientPath = path
	}

	if cfg.Upload {
		cfg.UploadPayload = bytes.Repeat([]byte("0"), cfg.UploadBytes)
	}
	return nil
}

func engineNames() []string {
	names := make([]string, 0, len(engineSpecs))
	for name := range engineSpecs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func loadResolvers(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	seen := make(map[string]bool)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		addr, ok := formatAddr(line)
		if ok && !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no valid resolvers found")
	}
	return out, sc.Err()
}

func formatAddr(line string) (string, bool) {
	if ip := net.ParseIP(line); ip != nil {
		return net.JoinHostPort(ip.String(), "53"), true
	}
	host, port, err := net.SplitHostPort(line)
	if err != nil || net.ParseIP(host) == nil {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}

func worker(port int, cfg *Config, jobs <-chan string, stats *scanStats, status *statusPrinter) {
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
		success := false
		for i := 0; i <= cfg.Retries; i++ {
			if try(resolver, port, cfg, client, status) {
				success = true
				break
			}
		}
		stats.Record(success)
	}
}

func try(resolver string, port int, cfg *Config, client *http.Client, status *statusPrinter) bool {
	args, err := buildEngineArgs(cfg, resolver, port)
	if err != nil {
		return false
	}

	cmd := exec.Command(cfg.ClientPath, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return false
	}

	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	time.Sleep(time.Duration(cfg.TunnelWait) * time.Millisecond)

	var result Result
	result.Resolver = resolver
	result.Download = "off"
	result.Upload = "off"
	result.Whois = "off"
	result.Probe = "off"

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
		latency, ok := doUploadCheck(client, cfg.UploadURL, cfg.UploadTimeout, cfg.UploadPayload)
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
		latency, org, country, ok := lookupResolverInfo(client, resolver, cfg.WhoisTimeout)
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
		latency, ok := doHTTPCheck(client, cfg.TestURL, cfg.Timeout, false)
		if ok {
			result.Probe = "ok"
			if bestPriority < 1 {
				result.LatencyMS = latency
				bestPriority = 1
			}
		}
	}

	if bestPriority == 0 {
		return false
	}

	printResult(cfg, result, status)
	return true
}

func printResult(cfg *Config, result Result, status *statusPrinter) {
	if status != nil {
		status.PrintResult(cfg, result)
		return
	}
	if cfg.JSON {
		_ = json.NewEncoder(os.Stdout).Encode(result)
		return
	}
	if cfg.Short {
		fmt.Println(formatShortResult(result, cfg.Colorize))
		return
	}

	fmt.Println(formatPlainTextResult(result, cfg.Colorize))
}

func formatLatency(latencyMs int64, colorize bool) string {
	latency := fmt.Sprintf("%dms", latencyMs)
	if !colorize {
		return latency
	}

	switch {
	case latencyMs <= 2000:
		return "\033[32m" + latency + "\033[0m"
	case latencyMs <= 6000:
		return "\033[33m" + latency + "\033[0m"
	default:
		return "\033[31m" + latency + "\033[0m"
	}
}

func formatPlainTextResult(result Result, colorize bool) string {
	line := fmt.Sprintf("%s %s", result.Resolver, formatLatency(result.LatencyMS, colorize))
	parts := []string{line}
	parts = append(parts, "download="+strconv.Quote(result.Download))
	parts = append(parts, "upload="+strconv.Quote(result.Upload))
	parts = append(parts, "whois="+strconv.Quote(result.Whois))
	parts = append(parts, "probe="+strconv.Quote(result.Probe))
	if result.Whois != "off" {
		parts = append(parts, "org="+strconv.Quote(result.Org))
		parts = append(parts, "country="+strconv.Quote(result.Country))
	}
	return strings.Join(parts, " ")
}

func formatShortResult(result Result, colorize bool) string {
	return fmt.Sprintf("%s %s", result.Resolver, formatLatency(result.LatencyMS, colorize))
}

func fileIsTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func startProgressReporter(status *statusPrinter, total int, stats *scanStats, startedAt time.Time) func() {
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		ticker := time.NewTicker(defaultProgressUpdateInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				doneCount, healthyCount := stats.Snapshot()
				status.UpdateProgress(fmt.Sprintf("progress | %d/%d | healthy=%d | failed=%d | elapsed=%s", doneCount, total, healthyCount, doneCount-healthyCount, formatElapsed(time.Since(startedAt))))
			case <-stop:
				return
			}
		}
	}()

	return func() {
		close(stop)
		<-done
		status.ClearProgress()
	}
}

func (s *scanStats) Record(success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.processed++
	if success {
		s.healthy++
	}
}

func (s *scanStats) Snapshot() (processed, healthy int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.processed, s.healthy
}

func newStatusPrinter(cfg *Config) *statusPrinter {
	return &statusPrinter{
		colorize:     cfg.LogColorize,
		liveProgress: !cfg.Quiet && cfg.LogColorize,
	}
}

func (s *statusPrinter) Log(level string, format string, args ...any) {
	if s == nil {
		logf(false, level, format, args...)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.clearProgressLocked()
	logf(s.colorize, level, format, args...)
	s.renderProgressLocked()
}

func (s *statusPrinter) UpdateProgress(line string) {
	if s == nil || !s.liveProgress {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.progressLine = line
	s.renderProgressLocked()
}

func (s *statusPrinter) ClearProgress() {
	if s == nil || !s.liveProgress {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.clearProgressLocked()
	s.progressLine = ""
}

func (s *statusPrinter) PrintResult(cfg *Config, result Result) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clearProgressLocked()
	if cfg.JSON {
		_ = json.NewEncoder(os.Stdout).Encode(result)
	} else {
		fmt.Println(formatPlainTextResult(result, cfg.Colorize))
	}
	s.renderProgressLocked()
}

func (s *statusPrinter) clearProgressLocked() {
	if !s.liveProgress || !s.progressSeen {
		return
	}

	_, _ = io.WriteString(os.Stderr, "\r\033[K")
	s.progressSeen = false
}

func (s *statusPrinter) renderProgressLocked() {
	if !s.liveProgress || s.progressLine == "" {
		return
	}

	_, _ = fmt.Fprintf(os.Stderr, "\r\033[K%s %s", formatLogLevel("INFO", s.colorize), s.progressLine)
	s.progressSeen = true
}

func enabledChecks(cfg *Config) string {
	checks := make([]string, 0, 4)
	if cfg.Probe {
		checks = append(checks, "probe")
	}
	if cfg.Download {
		checks = append(checks, "download")
	}
	if cfg.Upload {
		checks = append(checks, "upload")
	}
	if cfg.Whois {
		checks = append(checks, "whois")
	}
	return strings.Join(checks, ",")
}

func enabledTimeouts(cfg *Config) string {
	parts := make([]string, 0, 4)
	if cfg.Probe {
		parts = append(parts, fmt.Sprintf("probe=%ds", cfg.Timeout))
	}
	if cfg.Download {
		parts = append(parts, fmt.Sprintf("download=%ds", cfg.DownloadTimeout))
	}
	if cfg.Upload {
		parts = append(parts, fmt.Sprintf("upload=%ds", cfg.UploadTimeout))
	}
	if cfg.Whois {
		parts = append(parts, fmt.Sprintf("whois=%ds", cfg.WhoisTimeout))
	}
	return strings.Join(parts, ",")
}

func formatElapsed(duration time.Duration) string {
	if duration < time.Second {
		return "0s"
	}
	return duration.Truncate(time.Second).String()
}

func logf(colorize bool, level string, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", formatLogLevel(level, colorize), fmt.Sprintf(format, args...))
}

func formatLogLevel(level string, colorize bool) string {
	tag := "[" + level + "]"
	if !colorize {
		return tag
	}

	switch level {
	case "INFO":
		return "\033[32m" + tag + "\033[0m"
	case "WARN":
		return "\033[33m" + tag + "\033[0m"
	case "ERR":
		return "\033[31m" + tag + "\033[0m"
	default:
		return tag
	}
}

func doHTTPCheck(client *http.Client, targetURL string, timeoutSeconds int, drainBody bool) (int64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("Connection", "close")

	start := time.Now()
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

	return time.Since(start).Milliseconds(), true
}

func doUploadCheck(client *http.Client, targetURL string, timeoutSeconds int, payload []byte) (int64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return 0, false
	}
	req.Header.Set("Connection", "close")
	req.Header.Set("Content-Type", "application/octet-stream")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()

	return time.Since(start).Milliseconds(), true
}

func lookupResolverInfo(client *http.Client, resolver string, timeoutSeconds int) (int64, string, string, bool) {
	host, _, err := net.SplitHostPort(resolver)
	if err != nil {
		return 0, "unknown", "unknown", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, whoisURL+"/"+host, nil)
	if err != nil {
		return 0, "unknown", "unknown", false
	}
	req.Header.Set("Connection", "close")

	start := time.Now()
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
	return time.Since(start).Milliseconds(), org, country, true
}

func buildEngineArgs(cfg *Config, resolver string, port int) ([]string, error) {
	spec := engineSpecs[cfg.Engine]
	listenAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	extraArgs := cfg.ParsedArgs

	args := make([]string, 0, len(spec.DefaultArgs)+8)
	if spec.InsertArgsBeforeTail {
		tailSize := 2
		if len(spec.DefaultArgs) < tailSize {
			return nil, errors.New("invalid engine configuration")
		}
		args = append(args, spec.DefaultArgs[:len(spec.DefaultArgs)-tailSize]...)
		args = append(args, extraArgs...)
		args = append(args, spec.DefaultArgs[len(spec.DefaultArgs)-tailSize:]...)
	} else {
		args = append(args, spec.DefaultArgs...)
		args = append(args, extraArgs...)
	}
	return expandPlaceholders(args, placeholderValues(cfg, resolver, port, listenAddr))
}

func placeholderValues(cfg *Config, resolver string, port int, listenAddr string) map[string]string {
	return map[string]string{
		"{resolver}":    resolver,
		"{domain}":      cfg.Domain,
		"{listen}":      listenAddr,
		"{listen_host}": "127.0.0.1",
		"{listen_port}": strconv.Itoa(port),
	}
}

func expandPlaceholders(args []string, values map[string]string) ([]string, error) {
	expanded := make([]string, 0, len(args))
	for _, arg := range args {
		current := arg
		for key, value := range values {
			current = strings.ReplaceAll(current, key, value)
		}
		if strings.Contains(current, "{") && strings.Contains(current, "}") {
			return nil, fmt.Errorf("unknown placeholder in argument %q", arg)
		}
		expanded = append(expanded, current)
	}
	return expanded, nil
}

func splitCommandLine(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false

	flush := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}

	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
		}
	}

	if escaped {
		return nil, errors.New("unfinished escape sequence")
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}

	flush()
	return args, nil
}
