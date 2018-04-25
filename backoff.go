package main

import (
	"context"
	"log"
	"time"

	"math/rand"

	"github.com/rcrowley/go-metrics"
)

type backOff struct {
	wait, maxWait time.Duration
}

func (b *backOff) do(ctx context.Context, fn func() error) bool {
	return b.doTimed(ctx, metrics.NilTimer{}, fn)
}

func (b *backOff) doTimed(ctx context.Context, timer metrics.Timer, fn func() error) bool {
	wait := b.wait
	t := time.Now()
	err := fn()
	for errs := 0; err != nil; errs++ {
		if ctx.Err() != nil {
			return false
		}
		if wait = jitter(2*wait, 10); wait > b.maxWait {
			wait = b.maxWait
		}
		log.Printf("Pausing: %s attempt=%d pause=%s\n", err, errs, wait)
		select {
		case <-time.After(wait):
			t = time.Now()
			err = fn()
		case <-ctx.Done():
			return false
		}
	}
	timer.UpdateSince(t)
	return true
}

// jitter returns d with random jitter +/- up to limit percent.
func jitter(d time.Duration, limit int) time.Duration {
	j := time.Duration(int(d) * rand.Intn(limit) / 100)
	if rand.Intn(2) == 0 {
		j = -j
	}
	return (d + j).Round(time.Second)
}
