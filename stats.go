package chainload

import (
	"time"

	metrics "github.com/rcrowley/go-metrics"
	"go.uber.org/zap/zapcore"
)

var (
	latestBlockNumberTimer = metrics.GetOrRegisterTimer("timer/latestBlockNumber", nil)
	sendTxTimer            = metrics.GetOrRegisterTimer("timer/sendTx", nil)
	sendTxErrMeter         = metrics.GetOrRegisterMeter("meter/sendTx/err", nil)
	signTxTimer            = metrics.GetOrRegisterTimer("timer/signTx", nil)
	suggestGasPriceTimer   = metrics.GetOrRegisterTimer("timer/suggestGasPrice", nil)
	pendingBalanceAtTimer  = metrics.GetOrRegisterTimer("timer/pendingBalanceAt", nil)
	pendingNonceAtTimer    = metrics.GetOrRegisterTimer("timer/pendingNonceAt", nil)
)

// Report holds statistics for a stretch of time.
type Report struct {
	dur  time.Duration // Length of report.
	txs  int64         // Successful transaction sends.
	errs int64         // Failed transaction sends.
}

func (r *Report) MarshalLogObject(oe zapcore.ObjectEncoder) error {
	oe.AddDuration("duration", r.dur)
	oe.AddInt64("txs", r.txs)
	oe.AddInt64("errs", r.errs)
	oe.AddFloat64("tps", r.TPS())
	return nil
}

func (r *Report) TPS() float64 {
	return float64(r.txs) / r.dur.Seconds()
}

type Status struct {
	latest, recent, total Report
}

func (s *Status) MarshalLogObject(oe zapcore.ObjectEncoder) error {
	oe.AddObject("latest", &s.latest)
	oe.AddObject("recent", &s.recent)
	oe.AddObject("total", &s.total)
	return nil
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
func (r *Reports) Add(rep *Report) *Status {
	r.latest = rep

	r.recent[r.recIdx] = rep
	r.recIdx = (r.recIdx + 1) % 10

	r.total.dur += rep.dur
	r.total.txs += rep.txs
	r.total.errs += rep.errs

	return r.status()
}

func (r *Reports) status() *Status {
	var s Status
	s.latest = *r.latest
	for _, rec := range r.recent {
		if rec != nil {
			s.recent.dur += rec.dur
			s.recent.txs += rec.txs
			s.recent.errs += rec.errs
		}
	}
	s.total = r.total
	return &s
}
