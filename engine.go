package f35

import (
	"sort"
	"strconv"
)

type EngineSpec struct {
	DefaultBinary string
}

var engineSpecs = map[string]EngineSpec{
	"dnstt": {
		DefaultBinary: "dnstt-client",
	},
	"slipstream": {
		DefaultBinary: "slipstream-client",
	},
	"vaydns": {
		DefaultBinary: "vaydns-client",
	},
}

func SupportedEngines() []string {
	names := make([]string, 0, len(engineSpecs))
	for name := range engineSpecs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func buildEngineArgs(cfg *runtimeConfig, resolver string, port int) []string {
	listenPort := strconv.Itoa(port)
	listenAddr := "127.0.0.1:" + listenPort
	extraArgs := cfg.ExtraArgs

	switch cfg.Engine {
	case "dnstt":
		args := make([]string, 0, len(extraArgs)+4)
		args = append(args, "-udp", resolver)
		args = append(args, extraArgs...)
		args = append(args, cfg.Domain, listenAddr)
		return args
	case "slipstream":
		args := make([]string, 0, len(extraArgs)+8)
		args = append(args,
			"--tcp-listen-host", "127.0.0.1",
			"--tcp-listen-port", listenPort,
			"--resolver", resolver,
			"--domain", cfg.Domain,
		)
		args = append(args, extraArgs...)
		return args
	default:
		args := make([]string, 0, len(extraArgs)+6)
		args = append(args,
			"-domain", cfg.Domain,
			"-listen", listenAddr,
			"-udp", resolver,
		)
		args = append(args, extraArgs...)
		return args
	}
}
