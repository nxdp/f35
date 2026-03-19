package main

import (
	"bufio"
	"context"
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
	Engine        string
	ClientPath    string
	Domain        string
	ResolversFile string
	TestURL       string
	Proxy         string
	ProxyUser     string
	ProxyPass     string
	Args          string
	ParsedArgs    []string
	Workers       int
	Retries       int
	TunnelWait    int
	Timeout       int
	StartPort     int
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

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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

	jobs := make(chan string, cfg.Workers*2)
	var wg sync.WaitGroup

	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			worker(port, cfg, jobs)
		}(cfg.StartPort + i)
	}

	for _, r := range resolvers {
		jobs <- r
	}
	close(jobs)

	wg.Wait()
	return nil
}

func parseFlags() *Config {
	c := &Config{}

	flag.StringVar(&c.ResolversFile, "r", "", "Path to file containing resolvers (IP or IP:PORT per line)")
	flag.StringVar(&c.Engine, "e", "dnstt", fmt.Sprintf("Tunnel engine to use: %s", strings.Join(engineNames(), "|")))
	flag.StringVar(&c.ClientPath, "p", "", "Explicit path to client binary (optional)")
	flag.StringVar(&c.Domain, "d", "", "Tunnel domain (e.g., ns.example.com)")
	flag.StringVar(&c.Args, "a", "", "Extra engine CLI args; supports placeholders like {resolver}, {domain}, {listen}")
	flag.StringVar(&c.TestURL, "u", "http://www.google.com/gen_204", "HTTP URL to test through the tunnel")
	flag.StringVar(&c.Proxy, "x", "http", "Protocol to use when sending request through the tunnel: http|https|socks5|socks5h")
	flag.StringVar(&c.ProxyUser, "U", "", "Proxy username (if the tunnel exit requires auth)")
	flag.StringVar(&c.ProxyPass, "P", "", "Proxy password (if the tunnel exit requires auth)")
	flag.IntVar(&c.Workers, "w", 20, "Number of concurrent scanning workers")
	flag.IntVar(&c.Retries, "R", 0, "Number of retries per resolver after the first failure")
	flag.IntVar(&c.TunnelWait, "s", 1000, "Time to wait (ms) for tunnel establishment before testing HTTP")
	flag.IntVar(&c.Timeout, "t", 5, "HTTP request timeout in seconds")
	flag.IntVar(&c.StartPort, "l", 40000, "Starting local port for tunnel listeners")

	flag.Parse()

	c.Engine = strings.ToLower(strings.TrimSpace(c.Engine))
	c.Proxy = strings.ToLower(strings.TrimSpace(c.Proxy))
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

	if cfg.Workers < 1 {
		return errors.New("-w must be >= 1")
	}
	if cfg.Retries < 0 {
		return errors.New("-R must be >= 0")
	}
	if cfg.Timeout < 1 {
		return errors.New("-t must be >= 1")
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

func worker(port int, cfg *Config, jobs <-chan string) {
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
		for i := 0; i <= cfg.Retries; i++ {
			if try(resolver, port, cfg, client) {
				break
			}
		}
	}
}

func try(resolver string, port int, cfg *Config, client *http.Client) bool {
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", cfg.TestURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Connection", "close")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	fmt.Printf("%s %dms\n", resolver, time.Since(start).Milliseconds())
	return true
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
