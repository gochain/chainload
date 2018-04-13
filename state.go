package main

import "github.com/rcrowley/go-metrics"

type state metrics.Counter

type stateTracker struct {
	state
}

func (s *stateTracker) transition(to state) state {
	if s.state != nil {
		s.Dec(1)
	}
	last := s.state
	s.state = to
	if s.state != nil {
		s.Inc(1)
	}
	return last
}

var (
	seederCollectState     state = metrics.GetOrRegisterCounter("seeder/state/collect", nil)
	seederEnsureFundsState state = metrics.GetOrRegisterCounter("seeder/state/ensureFunds", nil)
	seederSeedState        state = metrics.GetOrRegisterCounter("seeder/state/seed", nil)
	seederUpdateNonceState state = metrics.GetOrRegisterCounter("seeder/state/updateNonce", nil)

	senderAssignState    state = metrics.GetOrRegisterCounter("sender/state/assign", nil)
	senderUpdateGasState state = metrics.GetOrRegisterCounter("sender/state/updateGas", nil)
	senderSendState      state = metrics.GetOrRegisterCounter("sender/state/send", nil)
	senderSeedState      state = metrics.GetOrRegisterCounter("sender/state/seed", nil)
)
