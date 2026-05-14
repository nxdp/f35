package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	f35 "github.com/nxdp/f35"
	"github.com/pelletier/go-toml/v2"
	pflag "github.com/spf13/pflag"
)

type fileConfig struct {
	ResolversFile   *string `toml:"resolvers_file"`
	Engine          *string `toml:"engine"`
	ClientPath      *string `toml:"client_path"`
	Domain          *string `toml:"domain"`
	Args            *string `toml:"args"`
	JSON            *bool   `toml:"json"`
	Quiet           *bool   `toml:"quiet"`
	Short           *bool   `toml:"short"`
	ProbeURL        *string `toml:"probe_url"`
	ProbeHTTPMethod *string `toml:"probe_http_method"`
	Probe           *bool   `toml:"probe"`
	Download        *bool   `toml:"download"`
	DownloadURL     *string `toml:"download_url"`
	Upload          *bool   `toml:"upload"`
	UploadURL       *string `toml:"upload_url"`
	UploadBytes     *int    `toml:"upload_bytes"`
	Whois           *bool   `toml:"whois"`
	Proxy           *string `toml:"proxy"`
	ProxyUser       *string `toml:"proxy_user"`
	ProxyPass       *string `toml:"proxy_pass"`
	Workers         *int    `toml:"workers"`
	Retries         *int    `toml:"retries"`
	Wait            *int    `toml:"wait"`
	ProbeTimeout    *int    `toml:"probe_timeout"`
	DownloadTimeout *int    `toml:"download_timeout"`
	UploadTimeout   *int    `toml:"upload_timeout"`
	WhoisTimeout    *int    `toml:"whois_timeout"`
	StartPort       *int    `toml:"start_port"`
}

func parseFlags() (f35.Config, cliOptions, error) {
	defaults := f35.DefaultConfig()
	fs := newFlagSet(defaults)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return f35.Config{}, cliOptions{}, err
	}

	cfg := defaults
	opts := cliOptions{}

	configPath, err := fs.GetString("config")
	if err != nil {
		return f35.Config{}, cliOptions{}, fmt.Errorf("read --config: %w", err)
	}
	configPath = strings.TrimSpace(configPath)
	if configPath != "" {
		fileCfg, err := loadFileConfig(configPath)
		if err != nil {
			return f35.Config{}, cliOptions{}, err
		}
		applyFileConfig(&cfg, &opts, fileCfg)
	}

	applyFlagOverrides(&cfg, &opts, fs)

	if opts.resolversFile == "" || strings.TrimSpace(cfg.Domain) == "" {
		return f35.Config{}, cliOptions{}, fmt.Errorf("--resolvers and --domain are required")
	}

	if opts.args != "" {
		extraArgs, err := splitCommandLine(opts.args)
		if err != nil {
			return f35.Config{}, cliOptions{}, fmt.Errorf("invalid --args: %w", err)
		}
		cfg.ExtraArgs = extraArgs
	}

	opts.colorize = !opts.json && fileIsTerminal(os.Stdout)
	opts.logColorize = fileIsTerminal(os.Stderr)
	return cfg, opts, nil
}

func loadFileConfig(path string) (fileConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileConfig{}, fmt.Errorf("read config file: %w", err)
	}
	defer f.Close()

	var cfg fileConfig
	if err := toml.NewDecoder(f).DisallowUnknownFields().Decode(&cfg); err != nil {
		return fileConfig{}, fmt.Errorf("read config file: %w", err)
	}
	return cfg, nil
}

func applyFileConfig(cfg *f35.Config, opts *cliOptions, fileCfg fileConfig) {
	applyString(&opts.resolversFile, fileCfg.ResolversFile)
	applyString(&cfg.Engine, fileCfg.Engine)
	applyString(&cfg.ClientPath, fileCfg.ClientPath)
	applyString(&cfg.Domain, fileCfg.Domain)
	applyString(&opts.args, fileCfg.Args)
	applyBool(&opts.json, fileCfg.JSON)
	applyBool(&opts.quiet, fileCfg.Quiet)
	applyBool(&opts.short, fileCfg.Short)
	applyString(&cfg.ProbeURL, fileCfg.ProbeURL)
	applyString(&cfg.ProbeHTTPMethod, fileCfg.ProbeHTTPMethod)
	applyBool(&cfg.Probe, fileCfg.Probe)
	applyBool(&cfg.Download, fileCfg.Download)
	applyString(&cfg.DownloadURL, fileCfg.DownloadURL)
	applyBool(&cfg.Upload, fileCfg.Upload)
	applyString(&cfg.UploadURL, fileCfg.UploadURL)
	applyInt(&cfg.UploadBytes, fileCfg.UploadBytes)
	applyBool(&cfg.Whois, fileCfg.Whois)
	applyString(&cfg.Proxy, fileCfg.Proxy)
	applyString(&cfg.ProxyUser, fileCfg.ProxyUser)
	applyString(&cfg.ProxyPass, fileCfg.ProxyPass)
	applyInt(&cfg.Workers, fileCfg.Workers)
	applyInt(&cfg.Retries, fileCfg.Retries)
	applyMillis(&cfg.TunnelWait, fileCfg.Wait)
	applySeconds(&cfg.ProbeTimeout, fileCfg.ProbeTimeout)
	applySeconds(&cfg.DownloadTimeout, fileCfg.DownloadTimeout)
	applySeconds(&cfg.UploadTimeout, fileCfg.UploadTimeout)
	applySeconds(&cfg.WhoisTimeout, fileCfg.WhoisTimeout)
	applyInt(&cfg.StartPort, fileCfg.StartPort)
}

func applyFlagOverrides(cfg *f35.Config, opts *cliOptions, fs *pflag.FlagSet) {
	applyChangedString(fs, "resolvers", &opts.resolversFile)
	applyChangedString(fs, "engine", &cfg.Engine)
	applyChangedString(fs, "client-path", &cfg.ClientPath)
	applyChangedString(fs, "domain", &cfg.Domain)
	applyChangedString(fs, "args", &opts.args)
	applyChangedBool(fs, "json", &opts.json)
	applyChangedBool(fs, "quiet", &opts.quiet)
	applyChangedBool(fs, "short", &opts.short)
	applyChangedString(fs, "probe-url", &cfg.ProbeURL)
	applyChangedString(fs, "probe-http-method", &cfg.ProbeHTTPMethod)
	applyChangedBool(fs, "probe", &cfg.Probe)
	applyChangedBool(fs, "download", &cfg.Download)
	applyChangedString(fs, "download-url", &cfg.DownloadURL)
	applyChangedBool(fs, "upload", &cfg.Upload)
	applyChangedString(fs, "upload-url", &cfg.UploadURL)
	applyChangedInt(fs, "upload-bytes", &cfg.UploadBytes)
	applyChangedBool(fs, "whois", &cfg.Whois)
	applyChangedString(fs, "proxy", &cfg.Proxy)
	applyChangedString(fs, "proxy-user", &cfg.ProxyUser)
	applyChangedString(fs, "proxy-pass", &cfg.ProxyPass)
	applyChangedInt(fs, "workers", &cfg.Workers)
	applyChangedInt(fs, "retries", &cfg.Retries)
	applyChangedMillis(fs, "wait", &cfg.TunnelWait)
	applyChangedSeconds(fs, "probe-timeout", &cfg.ProbeTimeout)
	applyChangedSeconds(fs, "download-timeout", &cfg.DownloadTimeout)
	applyChangedSeconds(fs, "upload-timeout", &cfg.UploadTimeout)
	applyChangedSeconds(fs, "whois-timeout", &cfg.WhoisTimeout)
	applyChangedInt(fs, "start-port", &cfg.StartPort)
}

func applyString(dst *string, src *string) {
	if src != nil {
		*dst = *src
	}
}

func applyBool(dst *bool, src *bool) {
	if src != nil {
		*dst = *src
	}
}

func applyInt(dst *int, src *int) {
	if src != nil {
		*dst = *src
	}
}

func applySeconds(dst *time.Duration, src *int) {
	if src != nil {
		*dst = time.Duration(*src) * time.Second
	}
}

func applyMillis(dst *time.Duration, src *int) {
	if src != nil {
		*dst = time.Duration(*src) * time.Millisecond
	}
}

func applyChangedString(fs *pflag.FlagSet, name string, dst *string) {
	if !fs.Lookup(name).Changed {
		return
	}
	value, _ := fs.GetString(name)
	*dst = value
}

func applyChangedBool(fs *pflag.FlagSet, name string, dst *bool) {
	if !fs.Lookup(name).Changed {
		return
	}
	value, _ := fs.GetBool(name)
	*dst = value
}

func applyChangedInt(fs *pflag.FlagSet, name string, dst *int) {
	if !fs.Lookup(name).Changed {
		return
	}
	value, _ := fs.GetInt(name)
	*dst = value
}

func applyChangedSeconds(fs *pflag.FlagSet, name string, dst *time.Duration) {
	if !fs.Lookup(name).Changed {
		return
	}
	value, _ := fs.GetInt(name)
	*dst = time.Duration(value) * time.Second
}

func applyChangedMillis(fs *pflag.FlagSet, name string, dst *time.Duration) {
	if !fs.Lookup(name).Changed {
		return
	}
	value, _ := fs.GetInt(name)
	*dst = time.Duration(value) * time.Millisecond
}

func newFlagSet(defaults f35.Config) *pflag.FlagSet {
	fs := pflag.NewFlagSet(os.Args[0], pflag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.String("resolvers", "", "Path to file containing resolvers (IP or IP:PORT per line)")
	fs.String("engine", defaults.Engine, fmt.Sprintf("Tunnel engine to use: %s", strings.Join(f35.SupportedEngines(), "|")))
	fs.String("client-path", defaults.ClientPath, "Explicit path to client binary (optional)")
	fs.StringP("config", "c", "", "Path to TOML config file")
	fs.String("domain", defaults.Domain, "Tunnel domain (e.g., ns.example.com)")
	fs.String("args", "", "Extra engine CLI args passed to the tunnel client as-is")
	fs.Bool("json", false, "Print one JSON object per result line")
	fs.Bool("quiet", false, "Suppress startup, progress, and completion logs")
	fs.Bool("short", false, "Print only IP:PORT and latency in plain text output")
	fs.String("probe-url", defaults.ProbeURL, "HTTP URL used for the probe request through the tunnel")
	fs.String("probe-http-method", defaults.ProbeHTTPMethod, "HTTP method used for the probe request: GET|HEAD")
	fs.Bool("probe", defaults.Probe, "Run a quick connectivity probe through the tunnel")
	fs.Bool("download", defaults.Download, "Run a real download test through the tunnel")
	fs.String("download-url", defaults.DownloadURL, "HTTP URL used for the download test")
	fs.Bool("upload", defaults.Upload, "Run a real upload test through the tunnel")
	fs.String("upload-url", defaults.UploadURL, "HTTP URL used for the upload test")
	fs.Int("upload-bytes", defaults.UploadBytes, "Number of bytes to send for the upload test")
	fs.Bool("whois", defaults.Whois, "Lookup resolver owner info and print organization and country")
	fs.String("proxy", defaults.Proxy, "Protocol to use when sending request through the tunnel: http|https|socks5|socks5h")
	fs.String("proxy-user", defaults.ProxyUser, "Proxy username (if the tunnel exit requires auth)")
	fs.String("proxy-pass", defaults.ProxyPass, "Proxy password (if the tunnel exit requires auth)")
	fs.Int("workers", defaults.Workers, "Number of concurrent scanning workers")
	fs.Int("retries", defaults.Retries, "Number of retries per resolver after the first failure")
	fs.Int("wait", int(defaults.TunnelWait/time.Millisecond), "Time to wait (ms) for tunnel establishment before testing HTTP")
	fs.Int("probe-timeout", int(defaults.ProbeTimeout/time.Second), "Probe request timeout in seconds")
	fs.Int("download-timeout", int(defaults.DownloadTimeout/time.Second), "Download request timeout in seconds")
	fs.Int("upload-timeout", int(defaults.UploadTimeout/time.Second), "Upload request timeout in seconds")
	fs.Int("whois-timeout", int(defaults.WhoisTimeout/time.Second), "WHOIS lookup timeout in seconds")
	fs.Int("start-port", defaults.StartPort, "Starting local port for tunnel listeners")

	return fs
}

func printUsage() {
	fs := newFlagSet(f35.DefaultConfig())
	fs.SetOutput(os.Stderr)
	_, _ = fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fs.PrintDefaults()
}
