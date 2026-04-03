package f35

import (
	"context"
	"net"
	"strconv"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/zmap/dns"
	"github.com/zmap/zdns/v2/src/zdns"
)

func filterAliveResolvers(cfg *runtimeConfig, hooks Hooks) ([]string, error) {
	total := len(cfg.Resolvers)
	if total == 0 {
		return nil, nil
	}

	workerCount := cfg.AliveThreads
	if total < workerCount {
		workerCount = total
	}

	resolverConfig := newAliveResolverConfig(cfg)
	jobs := make(chan string, workerCount*2)
	stats := newScanStats(progressStageAlive, total)
	resolvers := make([]*zdns.Resolver, 0, workerCount)
	for i := 0; i < workerCount; i++ {
		resolver, err := zdns.InitResolver(resolverConfig)
		if err != nil {
			for _, started := range resolvers {
				started.Close()
			}
			return nil, err
		}
		resolvers = append(resolvers, resolver)
	}

	aliveResolvers := make([]string, 0, total)
	var aliveMu sync.Mutex

	var wg sync.WaitGroup
	for _, resolver := range resolvers {
		wg.Add(1)
		go func(resolver *zdns.Resolver) {
			defer wg.Done()
			defer resolver.Close()
			aliveWorker(resolver, cfg, jobs, &aliveResolvers, &aliveMu, hooks, stats)
		}(resolver)
	}

	for _, resolver := range cfg.Resolvers {
		jobs <- resolver
	}
	close(jobs)

	wg.Wait()

	return aliveResolvers, nil
}

func newAliveResolverConfig(cfg *runtimeConfig) *zdns.ResolverConfig {
	resolverConfig := zdns.NewResolverConfig()
	resolverConfig.TransportMode = zdns.UDPOnly
	resolverConfig.IPVersionMode = zdns.IPv4OrIPv6
	resolverConfig.Timeout = cfg.AliveTimeout
	resolverConfig.NetworkTimeout = cfg.AliveTimeout
	resolverConfig.Retries = cfg.AliveRetries
	resolverConfig.LogLevel = log.PanicLevel
	return resolverConfig
}

func aliveWorker(resolver *zdns.Resolver, cfg *runtimeConfig, jobs <-chan string, aliveResolvers *[]string, aliveMu *sync.Mutex, hooks Hooks, stats *scanStats) {
	for resolverAddr := range jobs {
		alive := isResolverAlive(resolver, resolverAddr, cfg.AliveName)
		if alive {
			aliveMu.Lock()
			*aliveResolvers = append(*aliveResolvers, resolverAddr)
			aliveMu.Unlock()
		}

		progress := stats.Record(alive)
		if hooks.OnProgress != nil {
			hooks.OnProgress(progress)
		}
	}
}

func isResolverAlive(resolver *zdns.Resolver, resolverAddr string, lookupName string) bool {
	nameServer, err := parseAliveNameServer(resolverAddr)
	if err != nil {
		return false
	}

	_, _, status, err := resolver.ExternalLookup(
		context.Background(),
		&zdns.Question{Name: lookupName, Type: dns.TypeA, Class: dns.ClassINET},
		nameServer,
	)
	return isAliveStatus(status, err)
}

func parseAliveNameServer(resolverAddr string) (*zdns.NameServer, error) {
	host, portString, err := net.SplitHostPort(resolverAddr)
	if err != nil {
		return nil, err
	}

	port, err := strconv.ParseUint(portString, 10, 16)
	if err != nil {
		return nil, err
	}

	return &zdns.NameServer{
		IP:   net.ParseIP(host),
		Port: uint16(port),
	}, nil
}

func isAliveStatus(status zdns.Status, err error) bool {
	if err != nil {
		return false
	}
	switch status {
	case "", zdns.StatusTimeout, zdns.StatusIterTimeout:
		return false
	default:
		return true
	}
}
