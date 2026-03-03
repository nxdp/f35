package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	ResolversFile string
	Domain        string
	Proxy         string
	ProxyUser     string
	ProxyPass     string
	Workers       int
	TestURL       string
	TunnelWait    int
	Timeout       int
	StartPort     int
	ClientPath    string
}

func main() {
	cfg := Config{}
	flag.StringVar(&cfg.ResolversFile, "r", "", "Path to resolvers file")
	flag.StringVar(&cfg.Domain, "d", "", "Tunnel domain (e.g. ns.domain.tld)")
	flag.StringVar(&cfg.Proxy, "x", "http", "Proxy protocol for listener: http|https|socks5|socks5h")
	flag.StringVar(&cfg.ProxyUser, "U", "", "Optional proxy username")
	flag.StringVar(&cfg.ProxyPass, "P", "", "Optional proxy password")
	flag.IntVar(&cfg.Workers, "w", 20, "Concurrent workers")
	flag.StringVar(&cfg.TestURL, "u", "http://www.google.com/gen_204", "HTTP URL to test through tunnel")
	flag.IntVar(&cfg.TunnelWait, "s", 1000, "Milliseconds to wait for tunnel establishment before HTTP test")
	flag.IntVar(&cfg.Timeout, "t", 5, "HTTP request timeout in seconds")
	flag.IntVar(&cfg.StartPort, "l", 40000, "Starting local port for tunnel listeners")
	flag.Parse()
	cfg.Proxy = strings.ToLower(strings.TrimSpace(cfg.Proxy))

	if cfg.ResolversFile == "" || cfg.Domain == "" {
		flag.Usage()
		os.Exit(1)
	}
	switch cfg.Proxy {
	case "http", "https", "socks5", "socks5h":
	default:
		fmt.Fprintf(os.Stderr, "error: -x must be one of: http, https, socks5, socks5h\n")
		os.Exit(1)
	}
	if cfg.ProxyPass != "" && cfg.ProxyUser == "" {
		fmt.Fprintf(os.Stderr, "error: -P requires -U\n")
		os.Exit(1)
	}
	clientPath, err := exec.LookPath("slipstream-client")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: slipstream-client not found in PATH")
		os.Exit(1)
	}
	cfg.ClientPath = clientPath

	resolvers, err := loadResolvers(cfg.ResolversFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(resolvers) == 0 {
		fmt.Fprintf(os.Stderr, "error: no valid resolvers found in %s (expected IP or IP:PORT per line)\n", cfg.ResolversFile)
		os.Exit(1)
	}
	jobs := make(chan string, len(resolvers))
	for _, r := range resolvers {
		jobs <- r
	}
	close(jobs)

	var wg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			scan(port, &cfg, jobs)
		}(cfg.StartPort + i)
	}

	wg.Wait()
}

func loadResolvers(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	seen := make(map[string]bool)
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		entry, ok := parseResolver(line)
		if !ok {
			continue
		}
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	return out, sc.Err()
}

func parseResolver(line string) (string, bool) {
	if ip := net.ParseIP(line); ip != nil {
		return net.JoinHostPort(ip.String(), "53"), true
	}

	host, portStr, err := net.SplitHostPort(line)
	if err != nil {
		return "", false
	}
	if net.ParseIP(host) == nil {
		return "", false
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return "", false
	}
	return fmt.Sprintf("%s:%d", host, p), true
}

func scan(localPort int, cfg *Config, jobs <-chan string) {
	proxyURL, _ := url.Parse(fmt.Sprintf("%s://127.0.0.1:%d", cfg.Proxy, localPort))
	if cfg.ProxyUser != "" {
		if cfg.ProxyPass != "" {
			proxyURL.User = url.UserPassword(cfg.ProxyUser, cfg.ProxyPass)
		} else {
			proxyURL.User = url.User(cfg.ProxyUser)
		}
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:      1,
			IdleConnTimeout:   30 * time.Second,
			DisableKeepAlives: true,
		},
	}

	for resolver := range jobs {
		tryResolver(resolver, localPort, cfg, client)
	}
}

func tryResolver(resolver string, localPort int, cfg *Config, client *http.Client) {
	cmd := exec.Command(cfg.ClientPath,
		"--tcp-listen-host", "127.0.0.1",
		"--tcp-listen-port", strconv.Itoa(localPort),
		"--resolver", resolver,
		"--domain", cfg.Domain,
		"--keep-alive-interval", "200",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	time.Sleep(time.Duration(cfg.TunnelWait) * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", cfg.TestURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Connection", "close")
	req.Header.Set("Cache-Control", "no-cache")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 {
		fmt.Printf("%s %dms\n", resolver, time.Since(start).Milliseconds())
	}
}
