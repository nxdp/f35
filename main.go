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
	Workers       int
	TestURL       string
	Timeout       int
	StartPort     int
}

func main() {
	cfg := Config{}
	flag.StringVar(&cfg.ResolversFile, "resolvers", "", "Path to resolvers file")
	flag.StringVar(&cfg.Domain, "domain", "", "Tunnel domain (e.g. ns.domain.tld)")
	flag.IntVar(&cfg.Workers, "workers", 20, "Concurrent workers")
	flag.StringVar(&cfg.TestURL, "test-url", "http://www.google.com/gen_204", "HTTP URL to test through tunnel")
	flag.IntVar(&cfg.Timeout, "timeout", 5, "HTTP request timeout in seconds")
	flag.IntVar(&cfg.StartPort, "start-port", 40000, "Starting local port for tunnel listeners")
	flag.Parse()

	if cfg.ResolversFile == "" || cfg.Domain == "" {
		flag.Usage()
		os.Exit(1)
	}

	resolvers, err := loadResolvers(cfg.ResolversFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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
		host, portStr, err := net.SplitHostPort(line)
		if err != nil || net.ParseIP(host) == nil {
			continue
		}
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			continue
		}
		entry := fmt.Sprintf("%s:%d", host, p)
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	return out, sc.Err()
}

func scan(localPort int, cfg *Config, jobs <-chan string) {
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", localPort))
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
	cmd := exec.Command("slipstream-client",
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

	time.Sleep(1 * time.Second)

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
