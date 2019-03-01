package chainload

import metrics "github.com/rcrowley/go-metrics"

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
	seederCollectState     state = metrics.GetOrRegisterCounter("state/seeder/collect", nil)
	seederEnsureFundsState state = metrics.GetOrRegisterCounter("state/seeder/ensureFunds", nil)
	seederSeedState        state = metrics.GetOrRegisterCounter("state/seeder/seed", nil)
	seederUpdateNonceState state = metrics.GetOrRegisterCounter("state/seeder/updateNonce", nil)

	senderAssignState    state = metrics.GetOrRegisterCounter("state/sender/assign", nil)
	senderUpdateGasState state = metrics.GetOrRegisterCounter("state/sender/updateGas", nil)
	senderSendState      state = metrics.GetOrRegisterCounter("state/sender/send", nil)
	senderSeedState      state = metrics.GetOrRegisterCounter("state/sender/seed", nil)
)
