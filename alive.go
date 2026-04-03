package f35

import (
	"context"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/zmap/dns"
	"github.com/zmap/zdns/v2/src/zdns"
)

func filterAliveResolvers(cfg *runtimeConfig, hooks Hooks) ([]parsedResolver, error) {
	total := len(cfg.parsedResolvers)
	if total == 0 {
		return nil, nil
	}

	workerCount := cfg.AliveThreads
	if total < workerCount {
		workerCount = total
	}

	resolverConfig := newAliveResolverConfig(cfg)
	jobs := make(chan parsedResolver, workerCount*2)
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

	var wg sync.WaitGroup
	workerResults := make([][]parsedResolver, len(resolvers))
	for i, resolver := range resolvers {
		wg.Add(1)
		go func(index int, resolver *zdns.Resolver) {
			defer wg.Done()
			defer resolver.Close()
			workerResults[index] = aliveWorker(resolver, cfg, jobs, hooks, stats)
		}(i, resolver)
	}

	for _, resolver := range cfg.parsedResolvers {
		jobs <- resolver
	}
	close(jobs)

	wg.Wait()

	aliveResolvers := make([]parsedResolver, 0, total)
	for _, results := range workerResults {
		aliveResolvers = append(aliveResolvers, results...)
	}

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

func aliveWorker(resolver *zdns.Resolver, cfg *runtimeConfig, jobs <-chan parsedResolver, hooks Hooks, stats *scanStats) []parsedResolver {
	aliveResolvers := make([]parsedResolver, 0)
	question := zdns.Question{Name: cfg.AliveName, Type: dns.TypeA, Class: dns.ClassINET}

	for target := range jobs {
		alive := isResolverAlive(resolver, &question, target)
		if alive {
			aliveResolvers = append(aliveResolvers, target)
		}

		progress := stats.Record(alive)
		if hooks.OnProgress != nil {
			hooks.OnProgress(progress)
		}
	}

	return aliveResolvers
}

func isResolverAlive(resolver *zdns.Resolver, question *zdns.Question, target parsedResolver) bool {
	nameServer := zdns.NameServer{
		IP:   target.ip,
		Port: target.port,
	}
	_, _, status, err := resolver.ExternalLookup(
		context.Background(),
		question,
		&nameServer,
	)
	return isAliveStatus(status, err)
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
