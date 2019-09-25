package chainload

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/gochain/gochain/v3/accounts"
	"github.com/gochain/gochain/v3/common"
	"github.com/gochain/gochain/v3/core/types"
	"go.uber.org/zap"
)

// Seeder issues funds to sender accounts and collects funds from inactive accounts.
type Seeder struct {
	*Node
	lgr  *zap.Logger
	acct *accounts.Account

	nonce uint64

	stateTracker
}

func (s *Seeder) String() string {
	return fmt.Sprintf("node=%d seeder=%s", s.Number, s.acct.Address.Hex())
}

type SeedReq struct {
	Addr common.Address
	Resp chan<- error
}

func (s *Seeder) Run(ctx context.Context, done func()) {
	defer done()

	s.lgr = s.Node.lgr.With(zap.Stringer("account", s.acct.Address))
	s.lgr.Info("Starting seeder")

	defer s.transition(nil)
	s.transition(seederSeedState)

	bo := backOff{maxWait: 30 * time.Second, wait: 1 * time.Second, lgr: s.lgr}
	collect := time.NewTimer(randBetweenDur(5*time.Minute, 10*time.Minute))
	defer collect.Stop()
	for {
		s.transition(seederEnsureFundsState)
		var gasPrice *big.Int
		if !bo.doTimed(ctx, suggestGasPriceTimer, func() (err error) {
			gasPrice, err = s.SuggestGasPrice(ctx)
			if err != nil {
				err = fmt.Errorf("failed to get gas price\t%s err=%q", s, err)
			}
			return
		}) {
			return
		}
		fee := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(randBetween(s.gas, 2*s.gas)))
		amt := fee.Mul(fee, new(big.Int).SetUint64(1000))
		// Ensure we have enough funds to seed.
		if !bo.do(ctx, func() (err error) {
			var c *big.Int
			c, err = s.ensureFunds(ctx, s.lgr, amt)
			if err != nil {
				err = fmt.Errorf("failed to collect enough to seed\t%s collected=%d err=%q\n", s, c, err)
			}
			return
		}) {
			return
		}
		s.transition(seederSeedState)
		select {
		case <-ctx.Done():
			return
		case seed := <-s.SeedCh:
			// Seed the sender with funds.
			tx := types.NewTransaction(s.nonce, seed.Addr, amt, randBetween(s.gas, 2*s.gas), gasPrice, nil)
			t := time.Now()
			tx, err := s.SignTx(*s.acct, tx)
			if err != nil {
				s.lgr.Warn("Failed to sign tx", zap.Error(err))
				seed.Resp <- err
				continue
			}
			signTxTimer.UpdateSince(t)
			t = time.Now()
			err = s.SendTransaction(ctx, tx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				sendTxErrMeter.Mark(1)
				s.lgr.Warn("Failed to send seed tx", zap.Error(err))
				seed.Resp <- err
				var wait time.Duration
				if msg := err.Error(); nonceErr(msg) || knownTxErr(msg) {
					old := s.nonce
					s.transition(seederUpdateNonceState)
					if !bo.doTimed(ctx, pendingNonceAtTimer, func() (err error) {
						s.nonce, err = s.PendingNonceAt(ctx, s.acct.Address)
						if err != nil {
							err = fmt.Errorf("failed to get nonce\t%s err=%q", s, err)
						}
						return
					}) {
						return
					}
					s.lgr.Info("Updated nonce", zap.Uint64("nonce", s.nonce), zap.Uint64("old", old))
				} else if msg == "transaction pool limit reached" {
					wait = randBetweenDur(5*time.Second, 30*time.Second)
				} else if lowFundsErr(msg) {
					s.transition(seederCollectState)
					if c, err := s.collect(ctx, amt); err != nil {
						s.lgr.Warn("Refund collection failed", zapBig("collected", c), zap.Error(err))
					}
				} else {
					wait = randBetweenDur(5*time.Second, 30*time.Second)
				}
				if wait != 0 {
					s.lgr.Info("Pausing seeder", zap.Duration("wait", wait), zap.Error(err))
					select {
					case <-time.After(wait):
					case <-ctx.Done():
						return
					}
				}
			} else {
				sendTxTimer.UpdateSince(t)
				s.nonce++
				seed.Resp <- nil
			}
		case <-collect.C:
			s.transition(seederCollectState)
			// Collect more funds for a while.
			collectCtx, _ := context.WithTimeout(ctx, 10*time.Second)
			if c, err := s.collect(collectCtx, amt); err != nil {
				s.lgr.Warn("Refund collection failed", zapBig("collected", c), zap.Error(err))
			}
			s.transition(seederSeedState)
		}
	}

}

func (s *Seeder) ensureFunds(ctx context.Context, lgr *zap.Logger, ensure *big.Int) (*big.Int, error) {
	bo := backOff{maxWait: 30 * time.Second, wait: 1 * time.Second, lgr: s.lgr}
	var bal *big.Int
	if !bo.doTimed(ctx, pendingBalanceAtTimer, func() (err error) {
		bal, err = s.PendingBalanceAt(ctx, s.acct.Address)
		if err != nil {
			err = fmt.Errorf("failed to get balance\t%s err=%q", s, err)
		}
		return
	}) {
		return nil, ctx.Err()
	}
	lgr.Info("Got seeder balance", zapBig("balance", bal))
	if s.nonce == 0 {
		old := s.nonce
		if !bo.doTimed(ctx, pendingNonceAtTimer, func() (err error) {
			s.nonce, err = s.PendingNonceAt(ctx, s.acct.Address)
			if err != nil {
				err = fmt.Errorf("failed to get nonce\t%s err=%q", s, err)
			}
			return
		}) {
			return nil, ctx.Err()
		}
		lgr.Info("Updated nonce", zap.Uint64("nonce", s.nonce), zap.Uint64("old", old))
	}
	if bal.Cmp(ensure) == -1 {
		defer s.transition(s.transition(seederCollectState))
		return s.collect(ctx, ensure.Sub(ensure, bal))
	}
	return nil, nil
}

func (s *Seeder) collect(ctx context.Context, amount *big.Int) (*big.Int, error) {
	collected := new(big.Int)
	refundNextAcct := func() (*big.Int, error) {
		acct, nonce, err := s.Next(ctx, s.Number)
		if err != nil {
			return nil, err
		}
		if acct == nil {
			return nil, errors.New("no account available")
		}
		if nonce == 0 {
			t := time.Now()
			nonce, err = s.PendingNonceAt(ctx, acct.Address)
			if err != nil {
				s.Return(acct, s.Number, nonce)
				return nil, err
			}
			pendingBalanceAtTimer.UpdateSince(t)
		}
		c, err := s.refund(ctx, *acct, nonce, s.acct.Address)
		if err != nil {
			s.Return(acct, s.Number, nonce)
			return nil, err
		}
		s.Return(acct, s.Number, nonce+1)
		return c, nil
	}
	for collected.Cmp(amount) == -1 && ctx.Err() != nil {
		c, err := refundNextAcct()
		if err != nil {
			if ctx.Err() != nil {
				return collected, ctx.Err()
			}
			wait := 2 * time.Second
			s.lgr.Warn("Failed to refund account - Pausing before continuing", zap.Duration("wait", wait), zap.Error(err))
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return collected, ctx.Err()
			}
		}
		collected = collected.Add(collected, c)
	}
	return collected, nil
}
