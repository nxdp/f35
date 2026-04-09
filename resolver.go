package f35

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

type parsedResolver struct {
	addr string
	ip   net.IP
	port uint16
}

// LoadResolvers reads non-empty resolver lines from a file.
// Resolver normalization and validation are handled by Scan and ValidateConfig.
func LoadResolvers(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("no resolvers found")
	}
	return lines, nil
}

func parseResolvers(input []string) []parsedResolver {
	out := make([]parsedResolver, 0, len(input))
	seen := make(map[string]bool)
	for _, line := range input {
		resolver, ok := parseResolver(strings.TrimSpace(line))
		if ok && !seen[resolver.addr] {
			seen[resolver.addr] = true
			out = append(out, resolver)
		}
	}
	return out
}

func resolverAddrs(resolvers []parsedResolver) []string {
	out := make([]string, 0, len(resolvers))
	for _, resolver := range resolvers {
		out = append(out, resolver.addr)
	}
	return out
}

func parseResolver(line string) (parsedResolver, bool) {
	if line == "" {
		return parsedResolver{}, false
	}
	if ip := net.ParseIP(line); ip != nil {
		return parsedResolver{
			addr: net.JoinHostPort(ip.String(), "53"),
			ip:   ip,
			port: 53,
		}, true
	}
	host, port, err := net.SplitHostPort(line)
	if err != nil {
		return parsedResolver{}, false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return parsedResolver{}, false
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return parsedResolver{}, false
	}
	return parsedResolver{
		addr: net.JoinHostPort(ip.String(), port),
		ip:   ip,
		port: uint16(portNumber),
	}, true
}
