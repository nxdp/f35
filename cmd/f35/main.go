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
		flag.Usage()
		return err
	}

	resolvers, err := f35.LoadResolvers(opts.resolversFile)
	if err != nil {
		return err
	}
	cfg.Resolvers = resolvers

	if err := f35.ValidateConfig(cfg); err != nil {
		flag.Usage()
		return err
	}

	startedAt := time.Now()
	ui := newStatusUI(opts, startedAt, len(cfg.Resolvers))

	if !opts.quiet {
		ui.Log("INFO", "starting | resolvers=%d | workers=%d | engine=%s", len(cfg.Resolvers), cfg.Workers, cfg.Engine)
		ui.Log("INFO", "config | checks=%s | wait=%dms | timeouts=%s", enabledChecks(cfg), cfg.TunnelWait.Milliseconds(), enabledTimeouts(cfg))
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
		level := "INFO"
		if progress.Healthy == 0 {
			level = "WARN"
		}
		ui.Log(level, "completed | %d/%d | healthy=%d | failed=%d | elapsed=%s", progress.Processed, progress.Total, progress.Healthy, progress.Failed, formatElapsed(time.Since(startedAt)))
	}

	return nil
}

func parseFlags() (f35.Config, cliOptions, error) {
	cfg := f35.DefaultConfig()
	opts := cliOptions{}

	probeTimeout := int(cfg.ProbeTimeout / time.Second)
	downloadTimeout := int(cfg.DownloadTimeout / time.Second)
	uploadTimeout := int(cfg.UploadTimeout / time.Second)
	whoisTimeout := int(cfg.WhoisTimeout / time.Second)
	tunnelWait := int(cfg.TunnelWait / time.Millisecond)

	flag.StringVar(&opts.resolversFile, "r", "", "Path to file containing resolvers (IP or IP:PORT per line)")
	flag.StringVar(&cfg.Engine, "e", cfg.Engine, fmt.Sprintf("Tunnel engine to use: %s", strings.Join(f35.SupportedEngines(), "|")))
	flag.StringVar(&cfg.ClientPath, "p", "", "Explicit path to client binary (optional)")
	flag.StringVar(&cfg.Domain, "d", "", "Tunnel domain (e.g., ns.example.com)")
	flag.StringVar(&opts.args, "a", "", "Extra engine CLI args; supports placeholders like {resolver}, {domain}, {listen}")
	flag.BoolVar(&opts.json, "json", false, "Print one JSON object per result line")
	flag.BoolVar(&opts.quiet, "q", false, "Suppress startup and completion logs")
	flag.BoolVar(&opts.short, "short", false, "Print only IP:PORT and latency in plain text output")
	flag.StringVar(&cfg.ProbeURL, "u", cfg.ProbeURL, "HTTP URL used for the probe request through the tunnel")
	flag.BoolVar(&cfg.Probe, "probe", cfg.Probe, "Run a quick connectivity probe through the tunnel")
	flag.BoolVar(&cfg.Download, "download", false, "Run a real download test through the tunnel")
	flag.StringVar(&cfg.DownloadURL, "download-url", cfg.DownloadURL, "HTTP URL used for the download test")
	flag.BoolVar(&cfg.Upload, "upload", false, "Run a real upload test through the tunnel")
	flag.StringVar(&cfg.UploadURL, "upload-url", cfg.UploadURL, "HTTP URL used for the upload test")
	flag.IntVar(&cfg.UploadBytes, "upload-bytes", cfg.UploadBytes, "Number of bytes to send for the upload test")
	flag.BoolVar(&cfg.Whois, "whois", false, "Lookup resolver owner info and print organization and country")
	flag.StringVar(&cfg.Proxy, "x", cfg.Proxy, "Protocol to use when sending request through the tunnel: http|https|socks5|socks5h")
	flag.StringVar(&cfg.ProxyUser, "U", "", "Proxy username (if the tunnel exit requires auth)")
	flag.StringVar(&cfg.ProxyPass, "P", "", "Proxy password (if the tunnel exit requires auth)")
	flag.IntVar(&cfg.Workers, "w", cfg.Workers, "Number of concurrent scanning workers")
	flag.IntVar(&cfg.Retries, "R", cfg.Retries, "Number of retries per resolver after the first failure")
	flag.IntVar(&tunnelWait, "s", tunnelWait, "Time to wait (ms) for tunnel establishment before testing HTTP")
	flag.IntVar(&probeTimeout, "t", probeTimeout, "Probe request timeout in seconds")
	flag.IntVar(&downloadTimeout, "download-timeout", downloadTimeout, "Download request timeout in seconds")
	flag.IntVar(&uploadTimeout, "upload-timeout", uploadTimeout, "Upload request timeout in seconds")
	flag.IntVar(&whoisTimeout, "whois-timeout", whoisTimeout, "WHOIS lookup timeout in seconds")
	flag.IntVar(&cfg.StartPort, "l", cfg.StartPort, "Starting local port for tunnel listeners")

	flag.Parse()

	if opts.resolversFile == "" || strings.TrimSpace(cfg.Domain) == "" {
		return f35.Config{}, cliOptions{}, errors.New("-r and -d are required")
	}

	if opts.args != "" {
		extraArgs, err := splitCommandLine(opts.args)
		if err != nil {
			return f35.Config{}, cliOptions{}, fmt.Errorf("invalid -a: %w", err)
		}
		cfg.ExtraArgs = extraArgs
	}

	cfg.TunnelWait = time.Duration(tunnelWait) * time.Millisecond
	cfg.ProbeTimeout = time.Duration(probeTimeout) * time.Second
	cfg.DownloadTimeout = time.Duration(downloadTimeout) * time.Second
	cfg.UploadTimeout = time.Duration(uploadTimeout) * time.Second
	cfg.WhoisTimeout = time.Duration(whoisTimeout) * time.Second

	opts.colorize = !opts.json && fileIsTerminal(os.Stdout)
	opts.logColorize = fileIsTerminal(os.Stderr)

	return cfg, opts, nil
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
		ui.ClearProgress()
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
		"\r\033[K%s %d/%d | healthy=%d | failed=%d | elapsed=%s",
		formatLogLevel("INFO", ui.colorize),
		ui.progress.Processed,
		ui.progress.Total,
		ui.progress.Healthy,
		ui.progress.Failed,
		formatElapsed(time.Since(ui.startedAt)),
	)
	ui.progressSeen = true
}

func enabledChecks(cfg f35.Config) string {
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

func enabledTimeouts(cfg f35.Config) string {
	parts := make([]string, 0, 4)
	if cfg.Probe {
		parts = append(parts, fmt.Sprintf("probe=%ds", int(cfg.ProbeTimeout/time.Second)))
	}
	if cfg.Download {
		parts = append(parts, fmt.Sprintf("download=%ds", int(cfg.DownloadTimeout/time.Second)))
	}
	if cfg.Upload {
		parts = append(parts, fmt.Sprintf("upload=%ds", int(cfg.UploadTimeout/time.Second)))
	}
	if cfg.Whois {
		parts = append(parts, fmt.Sprintf("whois=%ds", int(cfg.WhoisTimeout/time.Second)))
	}
	return strings.Join(parts, ",")
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
