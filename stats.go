package main

import (
	"fmt"
	"time"

	"github.com/rcrowley/go-metrics"
)

var (
	latestBlockNumberTimer = metrics.GetOrRegisterTimer("latestBlockNumber", nil)
	sendTxTimer            = metrics.GetOrRegisterTimer("sendTx", nil)
	sendTxErrMeter         = metrics.GetOrRegisterMeter("sendTx/err", nil)
	signTxTimer            = metrics.GetOrRegisterTimer("signTx", nil)
	suggestGasPriceTimer   = metrics.GetOrRegisterTimer("suggestGasPrice", nil)
	pendingBalanceAtTimer  = metrics.GetOrRegisterTimer("pendingBalanceAt", nil)
	pendingNonceAtTimer    = metrics.GetOrRegisterTimer("pendingNonceAt", nil)
)

// Report holds statistics for a stretch of time.
type Report struct {
	dur  time.Duration // Length of report.
	txs  int64         // Successful transaction sends.
	errs int64         // Failed transaction sends.
}

func (r *Report) String() string {
	return fmt.Sprintf("dur=%s txs=%d errs=%d tps=%d",
		r.dur.Round(time.Second), r.txs, r.errs, r.txs/int64(r.dur/time.Second))
}

// Reporter tracks statistics and emits reports for an execution.
type Reporter interface {
	// Report generates a report since the last (or start).
	Report() *Report
}

func NewReporter() Reporter {
	return &reporter{
		lastTS: time.Now(),
	}
}

type reporter struct {
	// Last report.
	lastTS   time.Time // Must init with start for seed report to make sense.
	lastTxs  int64
	lastErrs int64
}

func (s *reporter) Report() *Report {
	now := time.Now()
	txs := sendTxTimer.Count()
	errs := sendTxErrMeter.Count()

	r := &Report{
		dur:  now.Sub(s.lastTS),
		txs:  txs - s.lastTxs,
		errs: errs - s.lastErrs,
	}
	s.lastTS = now
	s.lastTxs = txs
	s.lastErrs = errs

	return r
}

// Reports keeps a history of recent reports.
type Reports struct {
	latest *Report
	recent [10]*Report // Circular buffer or recent reports.
	recIdx int         // Index into recent to place next report.
	total  Report
}

// Add adds the report to the set of reports.
func (r *Reports) Add(rep *Report) {
	r.latest = rep

	r.recent[r.recIdx] = rep
	r.recIdx = (r.recIdx + 1) % 10

	r.total.dur += rep.dur
	r.total.txs += rep.txs
	r.total.errs += rep.errs
}

func (r *Reports) Status() (latest, recent, total Report) {
	latest = *r.latest
	for _, rec := range r.recent {
		if rec != nil {
			recent.dur += rec.dur
			recent.txs += rec.txs
			recent.errs += rec.errs
		}
	}
	total = r.total
	return
}
