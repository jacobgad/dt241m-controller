package controller

import (
	"context"
	"sync"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
)

// forEach stops feeding items once ctx ends; items already taken by a worker still run.
func forEach[T any](ctx context.Context, items []T, limit int, fn func(T)) {
	workers := max(1, min(limit, len(items)))
	queue := make(chan T)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range queue {
				if ctx.Err() == nil {
					fn(item)
				}
			}
		}()
	}
	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		queue <- item
	}
	close(queue)
	wg.Wait()
}

// Hit is a device that answered a probe.
type Hit struct {
	IP   string
	Info *dt241m.DeviceInfo
}

// ProbeOptions bounds a sweep.
type ProbeOptions struct {
	Concurrency int
	Timeout     time.Duration
	OnHit       func(Hit)
}

// Probe sends get_device_info_proav to every address with bounded concurrency and
// reports each answer through OnHit. Addresses that do not answer are simply skipped.
func Probe(ctx context.Context, client dt241m.Client, ips []string, opts ProbeOptions) {
	forEach(ctx, ips, opts.Concurrency, func(ip string) {
		probeCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
		info, err := client.GetDeviceInfo(probeCtx, ip)
		if err == nil && opts.OnHit != nil {
			opts.OnHit(Hit{IP: ip, Info: info})
		}
	})
}
