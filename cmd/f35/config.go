package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	f35 "github.com/nxdp/f35"
	pflag "github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type configBinding struct {
	key  string
	flag string
}

var configBindings = []configBinding{
	{key: "resolvers_file", flag: "resolvers"},
	{key: "engine", flag: "engine"},
	{key: "client_path", flag: "client-path"},
	{key: "domain", flag: "domain"},
	{key: "args", flag: "args"},
	{key: "json", flag: "json"},
	{key: "quiet", flag: "quiet"},
	{key: "short", flag: "short"},
	{key: "probe_url", flag: "probe-url"},
	{key: "probe", flag: "probe"},
	{key: "download", flag: "download"},
	{key: "download_url", flag: "download-url"},
	{key: "upload", flag: "upload"},
	{key: "upload_url", flag: "upload-url"},
	{key: "upload_bytes", flag: "upload-bytes"},
	{key: "whois", flag: "whois"},
	{key: "proxy", flag: "proxy"},
	{key: "proxy_user", flag: "proxy-user"},
	{key: "proxy_pass", flag: "proxy-pass"},
	{key: "workers", flag: "workers"},
	{key: "retries", flag: "retries"},
	{key: "wait", flag: "wait"},
	{key: "probe_timeout", flag: "probe-timeout"},
	{key: "download_timeout", flag: "download-timeout"},
	{key: "upload_timeout", flag: "upload-timeout"},
	{key: "whois_timeout", flag: "whois-timeout"},
	{key: "start_port", flag: "start-port"},
}

var allowedConfigKeys = func() map[string]struct{} {
	keys := make(map[string]struct{}, len(configBindings))
	for _, binding := range configBindings {
		keys[binding.key] = struct{}{}
	}
	return keys
}()

func parseFlags() (f35.Config, cliOptions, error) {
	defaults := f35.DefaultConfig()
	fs := newFlagSet(defaults)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return f35.Config{}, cliOptions{}, err
	}

	v, err := newConfig(fs)
	if err != nil {
		return f35.Config{}, cliOptions{}, err
	}

	cfg, opts, err := buildConfig(v, fs)
	if err != nil {
		return f35.Config{}, cliOptions{}, err
	}

	opts.colorize = !opts.json && fileIsTerminal(os.Stdout)
	opts.logColorize = fileIsTerminal(os.Stderr)
	return cfg, opts, nil
}

func newConfig(fs *pflag.FlagSet) (*viper.Viper, error) {
	v := viper.New()

	configPath, err := fs.GetString("config")
	if err != nil {
		return nil, fmt.Errorf("read --config: %w", err)
	}
	configPath = strings.TrimSpace(configPath)
	if configPath != "" {
		v.SetConfigFile(configPath)
		v.SetConfigType("toml")
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		if err := validateConfigKeys(v); err != nil {
			return nil, err
		}
	}

	return v, nil
}

func validateConfigKeys(v *viper.Viper) error {
	for _, key := range v.AllKeys() {
		if _, ok := allowedConfigKeys[strings.ToLower(key)]; !ok {
			return fmt.Errorf("unknown config key: %s", key)
		}
	}
	return nil
}

func buildConfig(v *viper.Viper, fs *pflag.FlagSet) (f35.Config, cliOptions, error) {
	defaults := f35.DefaultConfig()
	cfg := defaults
	opts := cliOptions{}

	getString := func(name string) string {
		value, _ := fs.GetString(name)
		return value
	}
	getBool := func(name string) bool {
		value, _ := fs.GetBool(name)
		return value
	}
	getInt := func(name string) int {
		value, _ := fs.GetInt(name)
		return value
	}

	applyString := func(key string, changed bool, value string, dst *string) {
		if changed {
			*dst = value
			return
		}
		if v.IsSet(key) {
			*dst = v.GetString(key)
		}
	}
	applyBool := func(key string, changed bool, value bool, dst *bool) {
		if changed {
			*dst = value
			return
		}
		if v.IsSet(key) {
			*dst = v.GetBool(key)
		}
	}
	applyInt := func(key string, changed bool, value int, dst *int) {
		if changed {
			*dst = value
			return
		}
		if v.IsSet(key) {
			*dst = v.GetInt(key)
		}
	}
	applySeconds := func(key string, changed bool, value int, dst *time.Duration) {
		if changed {
			*dst = time.Duration(value) * time.Second
			return
		}
		if v.IsSet(key) {
			*dst = time.Duration(v.GetInt(key)) * time.Second
		}
	}
	applyMillis := func(key string, changed bool, value int, dst *time.Duration) {
		if changed {
			*dst = time.Duration(value) * time.Millisecond
			return
		}
		if v.IsSet(key) {
			*dst = time.Duration(v.GetInt(key)) * time.Millisecond
		}
	}

	applyString("engine", fs.Lookup("engine").Changed, getString("engine"), &cfg.Engine)
	applyString("client_path", fs.Lookup("client-path").Changed, getString("client-path"), &cfg.ClientPath)
	applyString("domain", fs.Lookup("domain").Changed, getString("domain"), &cfg.Domain)
	applyString("probe_url", fs.Lookup("probe-url").Changed, getString("probe-url"), &cfg.ProbeURL)
	applyString("download_url", fs.Lookup("download-url").Changed, getString("download-url"), &cfg.DownloadURL)
	applyString("upload_url", fs.Lookup("upload-url").Changed, getString("upload-url"), &cfg.UploadURL)
	applyBool("probe", fs.Lookup("probe").Changed, getBool("probe"), &cfg.Probe)
	applyBool("download", fs.Lookup("download").Changed, getBool("download"), &cfg.Download)
	applyBool("upload", fs.Lookup("upload").Changed, getBool("upload"), &cfg.Upload)
	applyBool("whois", fs.Lookup("whois").Changed, getBool("whois"), &cfg.Whois)
	applyString("proxy", fs.Lookup("proxy").Changed, getString("proxy"), &cfg.Proxy)
	applyString("proxy_user", fs.Lookup("proxy-user").Changed, getString("proxy-user"), &cfg.ProxyUser)
	applyString("proxy_pass", fs.Lookup("proxy-pass").Changed, getString("proxy-pass"), &cfg.ProxyPass)
	applyInt("workers", fs.Lookup("workers").Changed, getInt("workers"), &cfg.Workers)
	applyInt("retries", fs.Lookup("retries").Changed, getInt("retries"), &cfg.Retries)
	applyMillis("wait", fs.Lookup("wait").Changed, getInt("wait"), &cfg.TunnelWait)
	applySeconds("probe_timeout", fs.Lookup("probe-timeout").Changed, getInt("probe-timeout"), &cfg.ProbeTimeout)
	applySeconds("download_timeout", fs.Lookup("download-timeout").Changed, getInt("download-timeout"), &cfg.DownloadTimeout)
	applySeconds("upload_timeout", fs.Lookup("upload-timeout").Changed, getInt("upload-timeout"), &cfg.UploadTimeout)
	applyInt("upload_bytes", fs.Lookup("upload-bytes").Changed, getInt("upload-bytes"), &cfg.UploadBytes)
	applySeconds("whois_timeout", fs.Lookup("whois-timeout").Changed, getInt("whois-timeout"), &cfg.WhoisTimeout)
	applyInt("start_port", fs.Lookup("start-port").Changed, getInt("start-port"), &cfg.StartPort)

	applyString("resolvers_file", fs.Lookup("resolvers").Changed, getString("resolvers"), &opts.resolversFile)
	applyString("args", fs.Lookup("args").Changed, getString("args"), &opts.args)
	applyBool("json", fs.Lookup("json").Changed, getBool("json"), &opts.json)
	applyBool("quiet", fs.Lookup("quiet").Changed, getBool("quiet"), &opts.quiet)
	applyBool("short", fs.Lookup("short").Changed, getBool("short"), &opts.short)

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

	return cfg, opts, nil
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
