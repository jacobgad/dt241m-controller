package controller

import (
	"context"
	"sync"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
)

// Hit is a device that answered a probe.
type Hit struct {
	IP   string
	Info *dt241m.DeviceInfo
}

// ProbeOptions bounds a sweep and exposes hooks for observation.
type ProbeOptions struct {
	Concurrency  int
	Timeout      time.Duration
	OnHit        func(Hit)
	OnProbeStart func(ip string, active int)
}

// Probe sends get_device_info_proav to every address with bounded concurrency.
// Addresses that do not answer are not errors; they are simply absent from the result.
func Probe(ctx context.Context, client dt241m.Client, ips []string, opts ProbeOptions) []Hit {
	var (
		mu     sync.Mutex
		hits   []Hit
		active int
		wg     sync.WaitGroup
		queue  = make(chan string)
	)
	workers := opts.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(ips) {
		workers = len(ips)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range queue {
				if ctx.Err() != nil {
					continue
				}
				mu.Lock()
				active++
				current := active
				mu.Unlock()
				if opts.OnProbeStart != nil {
					opts.OnProbeStart(ip, current)
				}
				probeCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
				info, err := client.GetDeviceInfo(probeCtx, ip)
				cancel()
				if err == nil {
					hit := Hit{IP: ip, Info: info}
					mu.Lock()
					hits = append(hits, hit)
					mu.Unlock()
					if opts.OnHit != nil {
						opts.OnHit(hit)
					}
				}
				mu.Lock()
				active--
				mu.Unlock()
			}
		}()
	}
	for _, ip := range ips {
		if ctx.Err() != nil {
			break
		}
		queue <- ip
	}
	close(queue)
	wg.Wait()
	return hits
}
