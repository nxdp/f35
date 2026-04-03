package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	f35 "github.com/nxdp/f35"
)

const defaultProgressUpdateInterval = time.Second

type cliOptions struct {
	resolversFile string
	args          string
	json          bool
	quiet         bool
	short         bool
	colorize      bool
	logColorize   bool
}

type statusUI struct {
	mu           sync.Mutex
	colorize     bool
	liveProgress bool
	progress     f35.Progress
	progressSeen bool
	startedAt    time.Time
}

func main() {
	if err := run(); err != nil {
		logf(fileIsTerminal(os.Stderr), "ERR", "%v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, opts, err := parseFlags()
	if err != nil {
		printUsage()
		return err
	}

	resolvers, err := f35.LoadResolvers(opts.resolversFile)
	if err != nil {
		return err
	}
	cfg.Resolvers = resolvers

	if err := f35.ValidateConfig(cfg); err != nil {
		printUsage()
		return err
	}

	startedAt := time.Now()
	ui := newStatusUI(opts, startedAt, len(cfg.Resolvers))
	initialStage := "e2e"
	if cfg.Alive {
		initialStage = "alive"
	}
	ui.UpdateProgress(f35.Progress{Stage: initialStage, Total: len(cfg.Resolvers)})

	if !opts.quiet {
		ui.Log("INFO", "starting | resolvers=%d | workers=%d | engine=%s", len(cfg.Resolvers), cfg.Workers, cfg.Engine)
	}

	var stopProgress func()
	if !opts.quiet && ui.liveProgress {
		stopProgress = startProgressReporter(ui)
	}

	err = f35.Scan(cfg, f35.Hooks{
		OnProgress: func(progress f35.Progress) {
			if !opts.quiet {
				ui.UpdateProgress(progress)
			}
		},
		OnResult: func(result f35.Result) {
			ui.PrintResult(result, opts)
		},
	})
	if stopProgress != nil {
		stopProgress()
	}
	if err != nil {
		return err
	}

	if !opts.quiet {
		progress := ui.Progress()
		primaryLabel := progressPrimaryLabel(progress.Stage)
		level := "INFO"
		if progress.Healthy == 0 {
			level = "WARN"
		}
		ui.Log(level, "completed | %d/%d | %s=%d | failed=%d | elapsed=%s", progress.Processed, progress.Total, primaryLabel, progress.Healthy, progress.Failed, formatElapsed(time.Since(startedAt)))
	}

	return nil
}

func parseFlags() (f35.Config, cliOptions, error) {
	cfg := f35.DefaultConfig()
	opts := cliOptions{}
	configPath := ""

	cfg, opts, configPath, err := parseCLIArgs(os.Args[1:], cfg, opts, configPath)
	if err != nil {
		return f35.Config{}, cliOptions{}, err
	}
	if configPath != "" {
		cfg = f35.DefaultConfig()
		opts = cliOptions{}
		if err := loadConfigFile(configPath, &cfg, &opts); err != nil {
			return f35.Config{}, cliOptions{}, err
		}
		cfg, opts, _, err = parseCLIArgs(os.Args[1:], cfg, opts, configPath)
		if err != nil {
			return f35.Config{}, cliOptions{}, err
		}
	}

	if opts.resolversFile == "" || strings.TrimSpace(cfg.Domain) == "" {
		return f35.Config{}, cliOptions{}, errors.New("-resolvers and -domain are required")
	}

	if opts.args != "" {
		extraArgs, err := splitCommandLine(opts.args)
		if err != nil {
			return f35.Config{}, cliOptions{}, fmt.Errorf("invalid -args: %w", err)
		}
		cfg.ExtraArgs = extraArgs
	}

	opts.colorize = !opts.json && fileIsTerminal(os.Stdout)
	opts.logColorize = fileIsTerminal(os.Stderr)

	return cfg, opts, nil
}

type timeoutFlags struct {
	dns      int
	probe    int
	download int
	upload   int
	whois    int
	wait     int
}

func parseCLIArgs(args []string, cfg f35.Config, opts cliOptions, configPath string) (f35.Config, cliOptions, string, error) {
	timeouts := timeoutFlags{
		dns:      int(cfg.AliveTimeout / time.Second),
		probe:    int(cfg.ProbeTimeout / time.Second),
		download: int(cfg.DownloadTimeout / time.Second),
		upload:   int(cfg.UploadTimeout / time.Second),
		whois:    int(cfg.WhoisTimeout / time.Second),
		wait:     int(cfg.TunnelWait / time.Millisecond),
	}

	fs := newFlagSet(&cfg, &opts, &configPath, &timeouts)
	if err := fs.Parse(args); err != nil {
		return f35.Config{}, cliOptions{}, "", err
	}

	cfg.TunnelWait = time.Duration(timeouts.wait) * time.Millisecond
	cfg.AliveTimeout = time.Duration(timeouts.dns) * time.Second
	cfg.ProbeTimeout = time.Duration(timeouts.probe) * time.Second
	cfg.DownloadTimeout = time.Duration(timeouts.download) * time.Second
	cfg.UploadTimeout = time.Duration(timeouts.upload) * time.Second
	cfg.WhoisTimeout = time.Duration(timeouts.whois) * time.Second

	return cfg, opts, configPath, nil
}

func newFlagSet(cfg *f35.Config, opts *cliOptions, configPath *string, timeouts *timeoutFlags) *flag.FlagSet {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&opts.resolversFile, "resolvers", opts.resolversFile, "Path to file containing resolvers (IP or IP:PORT per line)")
	fs.StringVar(&cfg.Engine, "engine", cfg.Engine, fmt.Sprintf("Tunnel engine to use: %s", strings.Join(f35.SupportedEngines(), "|")))
	fs.StringVar(&cfg.ClientPath, "client-path", cfg.ClientPath, "Explicit path to client binary (optional)")
	fs.StringVar(configPath, "config", *configPath, "Path to TOML config file")
	fs.StringVar(&cfg.Domain, "domain", cfg.Domain, "Tunnel domain (e.g., ns.example.com)")
	fs.StringVar(&opts.args, "args", opts.args, "Extra engine CLI args; supports placeholders like {resolver}, {domain}, {listen}")
	fs.BoolVar(&opts.json, "json", opts.json, "Print one JSON object per result line")
	fs.BoolVar(&opts.quiet, "quiet", opts.quiet, "Suppress startup, progress, and completion logs")
	fs.BoolVar(&opts.short, "short", opts.short, "Print only IP:PORT and latency in plain text output")
	fs.BoolVar(&cfg.Alive, "dns", cfg.Alive, "Prefilter resolvers with a direct UDP DNS query before the E2E scan")
	fs.StringVar(&cfg.AliveName, "dns-name", cfg.AliveName, "Domain name used for the DNS prefilter query")
	fs.IntVar(&cfg.AliveThreads, "dns-threads", cfg.AliveThreads, "Number of concurrent workers for the DNS prefilter")
	fs.IntVar(&cfg.AliveRetries, "dns-retries", cfg.AliveRetries, "Number of retries per resolver in the DNS prefilter")
	fs.IntVar(&timeouts.dns, "dns-timeout", timeouts.dns, "DNS prefilter timeout in seconds")
	fs.StringVar(&cfg.ProbeURL, "probe-url", cfg.ProbeURL, "HTTP URL used for the probe request through the tunnel")
	fs.BoolVar(&cfg.Probe, "probe", cfg.Probe, "Run a quick connectivity probe through the tunnel")
	fs.BoolVar(&cfg.Download, "download", cfg.Download, "Run a real download test through the tunnel")
	fs.StringVar(&cfg.DownloadURL, "download-url", cfg.DownloadURL, "HTTP URL used for the download test")
	fs.BoolVar(&cfg.Upload, "upload", cfg.Upload, "Run a real upload test through the tunnel")
	fs.StringVar(&cfg.UploadURL, "upload-url", cfg.UploadURL, "HTTP URL used for the upload test")
	fs.IntVar(&cfg.UploadBytes, "upload-bytes", cfg.UploadBytes, "Number of bytes to send for the upload test")
	fs.BoolVar(&cfg.Whois, "whois", cfg.Whois, "Lookup resolver owner info and print organization and country")
	fs.StringVar(&cfg.Proxy, "proxy", cfg.Proxy, "Protocol to use when sending request through the tunnel: http|https|socks5|socks5h")
	fs.StringVar(&cfg.ProxyUser, "proxy-user", cfg.ProxyUser, "Proxy username (if the tunnel exit requires auth)")
	fs.StringVar(&cfg.ProxyPass, "proxy-pass", cfg.ProxyPass, "Proxy password (if the tunnel exit requires auth)")
	fs.IntVar(&cfg.Workers, "workers", cfg.Workers, "Number of concurrent scanning workers")
	fs.IntVar(&cfg.Retries, "retries", cfg.Retries, "Number of retries per resolver after the first failure")
	fs.IntVar(&timeouts.wait, "wait", timeouts.wait, "Time to wait (ms) for tunnel establishment before testing HTTP")
	fs.IntVar(&timeouts.probe, "timeout", timeouts.probe, "Probe request timeout in seconds")
	fs.IntVar(&timeouts.download, "download-timeout", timeouts.download, "Download request timeout in seconds")
	fs.IntVar(&timeouts.upload, "upload-timeout", timeouts.upload, "Upload request timeout in seconds")
	fs.IntVar(&timeouts.whois, "whois-timeout", timeouts.whois, "WHOIS lookup timeout in seconds")
	fs.IntVar(&cfg.StartPort, "start-port", cfg.StartPort, "Starting local port for tunnel listeners")

	return fs
}

func printUsage() {
	cfg := f35.DefaultConfig()
	opts := cliOptions{}
	configPath := ""
	timeouts := timeoutFlags{
		dns:      int(cfg.AliveTimeout / time.Second),
		probe:    int(cfg.ProbeTimeout / time.Second),
		download: int(cfg.DownloadTimeout / time.Second),
		upload:   int(cfg.UploadTimeout / time.Second),
		whois:    int(cfg.WhoisTimeout / time.Second),
		wait:     int(cfg.TunnelWait / time.Millisecond),
	}

	fs := newFlagSet(&cfg, &opts, &configPath, &timeouts)
	fs.SetOutput(os.Stderr)
	_, _ = fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fs.PrintDefaults()
}

func newStatusUI(opts cliOptions, startedAt time.Time, total int) *statusUI {
	return &statusUI{
		colorize:     opts.logColorize,
		liveProgress: opts.logColorize && !opts.quiet,
		progress: f35.Progress{
			Total: total,
		},
		startedAt: startedAt,
	}
}

func startProgressReporter(ui *statusUI) func() {
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		ui.RenderProgress()
		ticker := time.NewTicker(defaultProgressUpdateInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ui.RenderProgress()
			case <-stop:
				return
			}
		}
	}()

	return func() {
		close(stop)
		<-done
		ui.StopLiveProgress()
	}
}

func (ui *statusUI) UpdateProgress(progress f35.Progress) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.progress = progress
}

func (ui *statusUI) Progress() f35.Progress {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return ui.progress
}

func (ui *statusUI) Log(level string, format string, args ...any) {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	ui.clearProgressLocked()
	logf(ui.colorize, level, format, args...)
	ui.renderProgressLocked()
}

func (ui *statusUI) PrintResult(result f35.Result, opts cliOptions) {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	ui.clearProgressLocked()
	switch {
	case opts.json:
		_ = json.NewEncoder(os.Stdout).Encode(result)
	case opts.short:
		fmt.Println(formatShortResult(result, opts.colorize))
	default:
		fmt.Println(formatPlainTextResult(result, opts.colorize))
	}
	ui.renderProgressLocked()
}

func (ui *statusUI) RenderProgress() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.renderProgressLocked()
}

func (ui *statusUI) ClearProgress() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearProgressLocked()
}

func (ui *statusUI) StopLiveProgress() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearProgressLocked()
	ui.liveProgress = false
}

func (ui *statusUI) clearProgressLocked() {
	if !ui.liveProgress || !ui.progressSeen {
		return
	}
	_, _ = io.WriteString(os.Stderr, "\r\033[K")
	ui.progressSeen = false
}

func (ui *statusUI) renderProgressLocked() {
	if !ui.liveProgress {
		return
	}
	_, _ = fmt.Fprintf(
		os.Stderr,
		"\r\033[K%s %s %d/%d | %s=%d | failed=%d | elapsed=%s",
		formatLogLevel("INFO", ui.colorize),
		progressStage(ui.progress.Stage),
		ui.progress.Processed,
		ui.progress.Total,
		progressPrimaryLabel(ui.progress.Stage),
		ui.progress.Healthy,
		ui.progress.Failed,
		formatElapsed(time.Since(ui.startedAt)),
	)
	ui.progressSeen = true
}

func progressStage(stage string) string {
	if stage == "alive" {
		return "dns"
	}
	return "e2e"
}

func progressPrimaryLabel(stage string) string {
	return "healthy"
}

func formatPlainTextResult(result f35.Result, colorize bool) string {
	line := fmt.Sprintf("%s %s", result.Resolver, formatLatency(result.LatencyMS, colorize))
	parts := []string{line}
	parts = append(parts, "download="+strconvQuote(result.Download))
	parts = append(parts, "upload="+strconvQuote(result.Upload))
	parts = append(parts, "whois="+strconvQuote(result.Whois))
	parts = append(parts, "probe="+strconvQuote(result.Probe))
	if result.Whois != "off" {
		parts = append(parts, "org="+strconvQuote(result.Org))
		parts = append(parts, "country="+strconvQuote(result.Country))
	}
	return strings.Join(parts, " ")
}

func formatShortResult(result f35.Result, colorize bool) string {
	return fmt.Sprintf("%s %s", result.Resolver, formatLatency(result.LatencyMS, colorize))
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

func fileIsTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
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

func strconvQuote(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
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
