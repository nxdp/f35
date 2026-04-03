package main

import (
	"fmt"
	"os"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	f35 "github.com/nxdp/f35"
)

type fileConfig struct {
	ResolversFile   *string `toml:"resolvers_file"`
	Engine          *string `toml:"engine"`
	ClientPath      *string `toml:"client_path"`
	Domain          *string `toml:"domain"`
	Args            *string `toml:"args"`
	DNS             *bool   `toml:"dns"`
	DNSName         *string `toml:"dns_name"`
	DNSTimeout      *int    `toml:"dns_timeout"`
	DNSRetries      *int    `toml:"dns_retries"`
	DNSThreads      *int    `toml:"dns_threads"`
	Probe           *bool   `toml:"probe"`
	ProbeURL        *string `toml:"probe_url"`
	ProbeTimeout    *int    `toml:"probe_timeout"`
	Download        *bool   `toml:"download"`
	DownloadURL     *string `toml:"download_url"`
	DownloadTimeout *int    `toml:"download_timeout"`
	Upload          *bool   `toml:"upload"`
	UploadURL       *string `toml:"upload_url"`
	UploadBytes     *int    `toml:"upload_bytes"`
	UploadTimeout   *int    `toml:"upload_timeout"`
	Whois           *bool   `toml:"whois"`
	WhoisTimeout    *int    `toml:"whois_timeout"`
	Proxy           *string `toml:"proxy"`
	ProxyUser       *string `toml:"proxy_user"`
	ProxyPass       *string `toml:"proxy_pass"`
	Workers         *int    `toml:"workers"`
	Retries         *int    `toml:"retries"`
	Wait            *int    `toml:"wait"`
	StartPort       *int    `toml:"start_port"`
	JSON            *bool   `toml:"json"`
	Short           *bool   `toml:"short"`
	Quiet           *bool   `toml:"quiet"`
}

func loadConfigFile(path string, cfg *f35.Config, opts *cliOptions) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()

	var data fileConfig
	decoder := toml.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return fmt.Errorf("decode config file: %w", err)
	}

	applyFileConfig(data, cfg, opts)
	return nil
}

func applyFileConfig(data fileConfig, cfg *f35.Config, opts *cliOptions) {
	if data.ResolversFile != nil {
		opts.resolversFile = *data.ResolversFile
	}
	if data.Engine != nil {
		cfg.Engine = *data.Engine
	}
	if data.ClientPath != nil {
		cfg.ClientPath = *data.ClientPath
	}
	if data.Domain != nil {
		cfg.Domain = *data.Domain
	}
	if data.Args != nil {
		opts.args = *data.Args
	}
	if data.DNS != nil {
		cfg.Alive = *data.DNS
	}
	if data.DNSName != nil {
		cfg.AliveName = *data.DNSName
	}
	if data.DNSTimeout != nil {
		cfg.AliveTimeout = time.Duration(*data.DNSTimeout) * time.Second
	}
	if data.DNSRetries != nil {
		cfg.AliveRetries = *data.DNSRetries
	}
	if data.DNSThreads != nil {
		cfg.AliveThreads = *data.DNSThreads
	}
	if data.Probe != nil {
		cfg.Probe = *data.Probe
	}
	if data.ProbeURL != nil {
		cfg.ProbeURL = *data.ProbeURL
	}
	if data.ProbeTimeout != nil {
		cfg.ProbeTimeout = time.Duration(*data.ProbeTimeout) * time.Second
	}
	if data.Download != nil {
		cfg.Download = *data.Download
	}
	if data.DownloadURL != nil {
		cfg.DownloadURL = *data.DownloadURL
	}
	if data.DownloadTimeout != nil {
		cfg.DownloadTimeout = time.Duration(*data.DownloadTimeout) * time.Second
	}
	if data.Upload != nil {
		cfg.Upload = *data.Upload
	}
	if data.UploadURL != nil {
		cfg.UploadURL = *data.UploadURL
	}
	if data.UploadBytes != nil {
		cfg.UploadBytes = *data.UploadBytes
	}
	if data.UploadTimeout != nil {
		cfg.UploadTimeout = time.Duration(*data.UploadTimeout) * time.Second
	}
	if data.Whois != nil {
		cfg.Whois = *data.Whois
	}
	if data.WhoisTimeout != nil {
		cfg.WhoisTimeout = time.Duration(*data.WhoisTimeout) * time.Second
	}
	if data.Proxy != nil {
		cfg.Proxy = *data.Proxy
	}
	if data.ProxyUser != nil {
		cfg.ProxyUser = *data.ProxyUser
	}
	if data.ProxyPass != nil {
		cfg.ProxyPass = *data.ProxyPass
	}
	if data.Workers != nil {
		cfg.Workers = *data.Workers
	}
	if data.Retries != nil {
		cfg.Retries = *data.Retries
	}
	if data.Wait != nil {
		cfg.TunnelWait = time.Duration(*data.Wait) * time.Millisecond
	}
	if data.StartPort != nil {
		cfg.StartPort = *data.StartPort
	}
	if data.JSON != nil {
		opts.json = *data.JSON
	}
	if data.Short != nil {
		opts.short = *data.Short
	}
	if data.Quiet != nil {
		opts.quiet = *data.Quiet
	}
}
