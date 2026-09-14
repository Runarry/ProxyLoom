package runner

import (
	"context"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"sync"
	"time"
)

func (c *Client) capacity() runnerprotocol.Slots {
	s := runnerprotocol.Slots{ConfigValidate: 1}
	if c.config.NetworkEnabled {
		s.Connectivity = 4
		s.DownloadThroughput = 1
	}
	return s
}
func (c *Client) runConcurrent(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer c.http.CloseIdleConnections()
	defer c.ready.Store(false)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	type completion struct {
		kind string
		err  error
	}
	completed := make(chan completion, 6)
	active := map[string]int32{}
	lastRegistration := time.Time{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		for len(completed) > 0 {
			out := <-completed
			active[out.kind]--
			if errors.Is(out.err, ErrUnsafeCleanup) {
				return out.err
			}
		}
		if time.Since(lastRegistration) >= 5*time.Second {
			if err := c.register(ctx); err != nil {
				c.ready.Store(false)
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					continue
				}
			}
			lastRegistration = time.Now()
		}
		slots := c.capacity()
		slots.ConfigValidate -= active["config_validate"]
		slots.Connectivity -= active["connectivity"]
		slots.DownloadThroughput -= active["download_throughput"]
		// Offline jobs retain the original 32-thread UID budget. Online jobs
		// use 128 shared threads, so these modes cannot overlap on one Runner.
		if active["config_validate"] > 0 {
			slots.Connectivity = 0
			slots.DownloadThroughput = 0
		}
		if active["connectivity"]+active["download_throughput"] > 0 {
			slots.ConfigValidate = 0
		}
		if slots.ConfigValidate+slots.Connectivity+slots.DownloadThroughput > 0 {
			var response runnerprotocol.Response[runnerprotocol.LeaseData]
			issued := time.Now()
			err := c.post(ctx, "/internal/v1/jobs/lease", runnerprotocol.LeaseRequest{RunnerID: c.config.RunnerID, AvailableSlots: slots}, &response)
			if err == nil && response.Data.Lease != nil {
				lease := response.Data.Lease
				if lease.Type != "config_validate" && lease.Type != "connectivity" && lease.Type != "download_throughput" {
					return ErrControlRejected
				}
				allowed := map[string]int32{"config_validate": slots.ConfigValidate, "connectivity": slots.Connectivity, "download_throughput": slots.DownloadThroughput}
				if allowed[lease.Type] <= 0 {
					return ErrControlRejected
				}
				active[lease.Type]++
				workers.Go(func() { completed <- completion{lease.Type, c.executeLeaseFrom(ctx, lease, issued)} })
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case out := <-completed:
			active[out.kind]--
			if errors.Is(out.err, ErrUnsafeCleanup) {
				return out.err
			}
		case <-ticker.C:
		}
	}
	return nil
}
