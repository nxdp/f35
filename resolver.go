package f35

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

func LoadResolvers(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var raw []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw = append(raw, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	resolvers := normalizeResolvers(raw)
	if len(resolvers) == 0 {
		return nil, fmt.Errorf("no valid resolvers found")
	}
	return resolvers, nil
}

func normalizeResolvers(input []string) []string {
	out := make([]string, 0, len(input))
	seen := make(map[string]bool)
	for _, line := range input {
		addr, ok := formatAddr(strings.TrimSpace(line))
		if ok && !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	return out
}

func formatAddr(line string) (string, bool) {
	if line == "" {
		return "", false
	}
	if ip := net.ParseIP(line); ip != nil {
		return net.JoinHostPort(ip.String(), "53"), true
	}
	host, port, err := net.SplitHostPort(line)
	if err != nil || net.ParseIP(host) == nil {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}
