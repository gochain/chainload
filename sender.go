package chainload

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"time"

	"github.com/gochain/gochain/v3/accounts"
	"github.com/gochain/gochain/v3/common"
	"github.com/gochain/gochain/v3/core/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Sender struct {
	*Node
	lgr       *zap.Logger
	amount    uint64
	cycle     time.Duration
	Number    int
	RateLimit time.Duration

	acct     *accounts.Account
	recv     []common.Address
	nonce    uint64
	gasPrice *big.Int

	stateTracker
}

func (s *Sender) String() string {
	return fmt.Sprintf("node=%d sender=%d acct=%s", s.Node.Number, s.Number, s.acct.Address.Hex())
}

// assignAcct assigns an account from AccountStore, refunding and replacing acct
// if it already exists.
func (s *Sender) assignAcct(ctx context.Context) {
	var old *accounts.Account
	if s.acct != nil {
		old = s.acct
		amount, err := s.refund(ctx, *s.acct, s.nonce, *s.AccountStore.RandSeed())
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.lgr.Warn("Failed to refund account", zap.Error(err))
		} else if amount.Cmp(big.NewInt(0)) == 1 {
			s.nonce++
			s.lgr.Info("Refunded account", zapBig("amount", amount))
		}
		s.AccountStore.Return(s.acct, s.Node.Number, s.nonce)
	}
	bo := backOff{maxWait: 30 * time.Second, wait: 1 * time.Second, lgr: s.lgr}

	if !bo.do(ctx, func() (err error) {
		s.acct, s.nonce, err = s.AccountStore.Next(ctx, s.Node.Number)
		s.setLgr()
		if err != nil {
			err = fmt.Errorf("failed to assign sender account\tsender=%d err=%q", s.Number, err)
		}
		return
	}) {
		return
	}
	if s.acct == nil {
		if !bo.do(ctx, func() (err error) {
			s.acct, err = s.AccountStore.New(ctx)
			s.nonce = 0
			s.setLgr()
			if err != nil {
				err = fmt.Errorf("failed to create sender account\tsender=%d err=%q", s.Number, err)
			}
			return
		}) {
			return
		}
	} else {
		if !bo.doTimed(ctx, pendingNonceAtTimer, func() (err error) {
			s.nonce, err = s.Client.PendingNonceAt(ctx, s.acct.Address)
			if err != nil {
				err = fmt.Errorf("failed to get nonce\t%s err=%q", s, err)
			}
			return
		}) {
			return
		}
	}

	var bal *big.Int
	if !bo.doTimed(ctx, pendingBalanceAtTimer, func() (err error) {
		bal, err = s.PendingBalanceAt(ctx, s.acct.Address)
		if err != nil {
			err = fmt.Errorf("failed to get sender balance\t%s err=%q", s, err)
		}
		return
	}) {
		return
	}
	if old != nil {
		s.lgr.Info("Changed account", zapBig("balance", bal), zap.Stringer("old", old.Address))
	} else {
		s.lgr.Info("Assigned account", zapBig("balance", bal))
	}

	fee := new(big.Int).Mul(s.gasPrice, new(big.Int).SetUint64(s.gas))
	need := fee.Mul(fee, new(big.Int).SetUint64(1000))
	if bal.Cmp(need) == -1 {
		diff := new(big.Int).Sub(need, bal)
		s.transition(senderSeedState)
		if !bo.do(ctx, func() error {
			err := s.requestSeed(ctx, diff)
			if err != nil {
				return fmt.Errorf("failed to seed account\t%s err=%q", s, err)
			}
			return nil
		}) {
			return
		}
		if _, err := waitBlocks(ctx, s.lgr, s.Client, 5); err != nil {
			return
		}
		s.lgr.Info("Seeded account", zapBig("amount", diff), zapBig("balance", need))
		s.transition(senderAssignState)
	}

	if !bo.do(ctx, func() error {
		s.recv = s.AccountStore.NextRecv(s.acct.Address, rand.Intn(10)+1)
		if len(s.recv) == 0 {
			return fmt.Errorf("failed to assign sender receivers\t%s receivers=%v", s, receivers(s.recv))
		}
		return nil
	}) {
		return
	}
	s.lgr.Info("Assigned receivers", zap.Array("receivers", receivers(s.recv)))
}

type receivers []common.Address

func (r receivers) MarshalLogArray(oe zapcore.ArrayEncoder) error {
	for i := range r {
		oe.AppendString(r[i].Str())
	}
	return nil
}

func (r receivers) String() string {
	var b strings.Builder
	for i := range r {
		b.WriteString(r[i].Hex())
		if i < len(r)-1 {
			b.WriteString(",")
		}
	}
	return b.String()
}

func (s *Sender) requestSeed(ctx context.Context, amount *big.Int) error {
	resp := make(chan error)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.SeedCh <- SeedReq{
		//TODO use amount
		Addr: s.acct.Address,
		Resp: resp,
	}:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-resp:
		return err
	}
}

func (s *Sender) setLgr() {
	if s.acct == nil {
		s.lgr = s.Node.lgr.With(zap.String("account", "none"))
	} else {
		s.lgr = s.Node.lgr.With(zap.Stringer("account", s.acct.Address))
	}
}

func (s *Sender) Send(ctx context.Context, txs <-chan struct{}, done func()) {
	s.setLgr()
	defer func() {
		for range txs {
		}
		s.transition(nil)
		done()
	}()
	s.transition(senderUpdateGasState)
	s.updateGasPrice(ctx)
	if ctx.Err() != nil {
		return
	}
	s.transition(senderAssignState)
	s.assignAcct(ctx)
	if ctx.Err() != nil {
		return
	}
	s.transition(senderSendState)

	newAcct := time.NewTimer(randBetweenDur(s.cycle, 2*s.cycle))
	defer newAcct.Stop()
	updateGas := time.NewTimer(randBetweenDur(time.Minute, 2*time.Minute))
	defer newAcct.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-newAcct.C:
			s.transition(senderAssignState)
			s.assignAcct(ctx)
			s.transition(senderSendState)
		case <-updateGas.C:
			s.transition(senderUpdateGasState)
			s.updateGasPrice(ctx)
			s.transition(senderSendState)
		case <-txs:
			s.send(ctx)
		}
	}
}

func (s *Sender) updateGasPrice(ctx context.Context) {
	bo := backOff{maxWait: 30 * time.Second, wait: 1 * time.Second, lgr: s.lgr}
	_ = bo.doTimed(ctx, suggestGasPriceTimer, func() (err error) {
		s.gasPrice, err = s.Client.SuggestGasPrice(ctx)
		if err != nil {
			err = fmt.Errorf("failed to get gas price\tsender=%d err=%q\n", s.Number, err)
		}
		return
	})
}

func (s *Sender) send(ctx context.Context) {
	recv := s.recv[int(s.nonce)%len(s.recv)]
	gp := s.gasPrice.Uint64()
	if rand.Intn(2) == 0 {
		gp = randBetween(gp, gp*2)
	}
	amount := new(big.Int).SetUint64(randBetween(s.amount, 2*s.amount))
	tx := types.NewTransaction(s.nonce, recv, amount, randBetween(s.gas, 2*s.gas), new(big.Int).SetUint64(gp), nil)
	t := time.Now()
	tx, err := s.AccountStore.SignTx(*s.acct, tx)
	if err != nil {
		s.lgr.Warn("Failed to sign tx", zap.Error(err))
		s.transition(senderAssignState)
		s.assignAcct(ctx)
		s.transition(senderSendState)
		return
	}
	signTxTimer.UpdateSince(t)
	t = time.Now()
	err = s.Client.SendTransaction(ctx, tx)
	if err == nil {
		sendTxTimer.UpdateSince(t)
		s.nonce++

		select {
		case <-time.After(s.RateLimit):
		case <-ctx.Done():
			return
		}

		return
	}
	if ctx.Err() != nil {
		return
	}
	sendTxErrMeter.Mark(1)
	var wait time.Duration
	if msg := err.Error(); nonceErr(msg) {
		s.lgr.Warn("Failed to send - updating nonce", zap.Error(err))
		old := s.nonce
		bo := backOff{maxWait: 30 * time.Second, wait: 1 * time.Second, lgr: s.lgr}
		if !bo.doTimed(ctx, pendingNonceAtTimer, func() (err error) {
			s.nonce, err = s.Client.PendingNonceAt(ctx, s.acct.Address)
			if err != nil {
				err = fmt.Errorf("failed to get nonce\t%s err=%q", s, err)
			}
			return
		}) {
			return
		}
		s.lgr.Info("Updated nonce", zap.Uint64("nonce", s.nonce), zap.Uint64("old", old))
		return
	} else if msg == "transaction pool limit reached" {
		wait = randBetweenDur(5*time.Second, 2*time.Minute)
	} else if knownTxErr(msg) || lowFundsErr(msg) {
		s.lgr.Info("Abandoning account", zap.Error(err))
		s.transition(senderAssignState)
		s.assignAcct(ctx)
		s.transition(senderSendState)
		return
	} else {
		wait = randBetweenDur(5*time.Second, 30*time.Second)
	}
	if wait == 0 {
		s.lgr.Warn("Failed to send", zap.Error(err))
	}
	if wait != 0 {
		s.lgr.Info("Pausing sender", zap.Duration("wait", wait), zap.Error(err))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
	return
}
