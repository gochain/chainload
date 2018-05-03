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
		if wait = randBetweenDur(3/2*wait, 5/2*wait); wait > b.maxWait {
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

func randBetweenDur(start, end time.Duration) time.Duration {
	return start + time.Duration(rand.Int63n(int64(end-start)))
}

func randBetween(start, end uint64) uint64 {
	return start + uint64(rand.Int63n(int64(end-start)))
}
