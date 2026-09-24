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

// ProbeOptions bounds a sweep.
type ProbeOptions struct {
	Concurrency int
	Timeout     time.Duration
	OnHit       func(Hit)
}

// Probe sends get_device_info_proav to every address with bounded concurrency and
// reports each answer through OnHit. Addresses that do not answer are simply skipped.
func Probe(ctx context.Context, client dt241m.Client, ips []string, opts ProbeOptions) {
	workers := max(1, min(opts.Concurrency, len(ips)))
	queue := make(chan string)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range queue {
				if ctx.Err() != nil {
					continue
				}
				probeCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
				info, err := client.GetDeviceInfo(probeCtx, ip)
				cancel()
				if err == nil && opts.OnHit != nil {
					opts.OnHit(Hit{IP: ip, Info: info})
				}
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
}
