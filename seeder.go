package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"time"

	"github.com/gochain-io/gochain/accounts"
	"github.com/gochain-io/gochain/common"
	"github.com/gochain-io/gochain/core/types"
)

// Seeder issues funds to sender accounts and collects funds from inactive accounts.
type Seeder struct {
	*Node
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
	defer s.transition(nil)
	s.transition(seederSeedState)

	log.Printf("Running seeder\t%s\n", s)
	bo := backOff{maxWait: 30 * time.Second, wait: 1 * time.Second}
	collectTick := time.Tick(time.Second * time.Duration(10+rand.Intn(10)))
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
		// Pay double.
		gasPrice.Add(gasPrice, gasPrice)
		fee := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(jitter(config.gas, 10)))
		amt := fee.Mul(fee, new(big.Int).SetUint64(1000))
		// Ensure we have enough funds to seed.
		if !bo.do(ctx, func() (err error) {
			var c uint64
			c, err = s.ensureFunds(ctx, amt.Uint64())
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
			tx := types.NewTransaction(s.nonce, seed.Addr, amt, jitter(config.gas, 10), gasPrice, nil)
			t := time.Now()
			tx, err := s.SignTx(*s.acct, tx)
			if err != nil {
				log.Printf("Failed to sign tx\t%s err=%q\n", s, err)
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
				log.Printf("Failed to send seed tx\t%s err=%q\n", s, err)
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
					log.Printf("Updated nonce\t%s old=%d new=%d\n", s, old, s.nonce)
				} else if msg == "transaction pool limit reached" {
					wait = jitterDur(time.Minute, 80)
				} else if lowFundsErr(msg) {
					s.transition(seederCollectState)
					if c, err := s.collect(ctx, amt.Uint64()); err != nil {
						log.Printf("Refund collection failed\t%s collected=%d err=%q\n", s, c, err)
					}
				} else {
					wait = jitterDur(30*time.Second, 50)
				}
				if wait != 0 {
					log.Printf("Pausing seeder\t%s pause=%s err=%q\n", s, wait, err)
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
		case <-collectTick:
			s.transition(seederCollectState)
			// Collect more funds for a while.
			collectCtx, _ := context.WithTimeout(ctx, 10*time.Second)
			if c, err := s.collect(collectCtx, amt.Uint64()); err != nil {
				log.Printf("Refund collection failed\t%s collected=%d err=%q\n", s, c, err)
			}
			collectTick = time.Tick(time.Second * time.Duration(10+rand.Intn(10)))
			s.transition(seederSeedState)
		}
	}

}

func (s *Seeder) ensureFunds(ctx context.Context, ensure uint64) (uint64, error) {
	bo := backOff{maxWait: 30 * time.Second, wait: 1 * time.Second}
	var pb *big.Int
	if !bo.doTimed(ctx, pendingBalanceAtTimer, func() (err error) {
		pb, err = s.PendingBalanceAt(ctx, s.acct.Address)
		if err != nil {
			err = fmt.Errorf("failed to get balance\t%s err=%q", s, err)
		}
		return
	}) {
		return 0, ctx.Err()
	}
	bal := pb.Uint64()
	if config.verbose {
		log.Printf("Got seed balance\t%s balance=%d\n", s, bal)
	}
	if s.nonce == 0 {
		old := s.nonce
		if !bo.doTimed(ctx, pendingNonceAtTimer, func() (err error) {
			s.nonce, err = s.PendingNonceAt(ctx, s.acct.Address)
			if err != nil {
				err = fmt.Errorf("failed to get nonce\t%s err=%q", s, err)
			}
			return
		}) {
			return 0, ctx.Err()
		}
		log.Printf("Updated nonce\t%s old=%d new=%d\n", s, old, s.nonce)
	}
	if bal < ensure {
		defer s.transition(s.transition(seederCollectState))
		return s.collect(ctx, ensure-bal)
	}
	return 0, nil
}

func (s *Seeder) collect(ctx context.Context, amount uint64) (uint64, error) {
	var collected uint64
	refundNextAcct := func() (uint64, error) {
		acct, nonce, err := s.Next(ctx, s.Number)
		if err != nil {
			return 0, err
		}
		if acct == nil {
			return 0, errors.New("no account available")
		}
		if nonce == 0 {
			t := time.Now()
			nonce, err = s.PendingNonceAt(ctx, acct.Address)
			if err != nil {
				s.Return(acct, s.Number, nonce)
				return 0, err
			}
			pendingBalanceAtTimer.UpdateSince(t)
		}
		c, err := s.refund(ctx, *acct, nonce, s.acct.Address)
		if err != nil {
			s.Return(acct, s.Number, nonce)
			return 0, err
		}
		s.Return(acct, s.Number, nonce+1)
		return c, nil
	}
	for collected < amount && ctx.Err() != nil {
		c, err := refundNextAcct()
		if err != nil {
			if ctx.Err() != nil {
				return collected, ctx.Err()
			}
			wait := 2 * time.Second
			log.Printf("Failed to refund account - Pausing before continuing\t%s pause=%s err=%q\n", s, wait, err)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return collected, ctx.Err()
			}
		}
		collected += c
	}
	return collected, nil
}
