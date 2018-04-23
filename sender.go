package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"strings"
	"time"

	"github.com/gochain-io/gochain/accounts"
	"github.com/gochain-io/gochain/common"
	"github.com/gochain-io/gochain/core/types"
)

type Sender struct {
	*Node
	Number int

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
	if s.acct != nil {
		amount, err := s.refund(ctx, *s.acct, s.nonce, *s.AccountStore.RandSeed())
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("Failed to refund account\t%s err=%q\n", s, err)
		} else if amount > 0 {
			s.nonce++
			log.Printf("Refunded account\t%s amount=%d", s, amount)
		}
		s.AccountStore.Return(s.acct, s.Node.Number, s.nonce)
	}
	bo := backOff{maxWait: 30 * time.Second, wait: 1 * time.Second}

	if !bo.do(ctx, func() (err error) {
		s.acct, s.nonce, err = s.AccountStore.Next(ctx, s.Node.Number)
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
	log.Printf("Assigned sender account\t%s\n", s)

	var pb *big.Int
	if !bo.doTimed(ctx, pendingBalanceAtTimer, func() (err error) {
		pb, err = s.PendingBalanceAt(ctx, s.acct.Address)
		if err != nil {
			err = fmt.Errorf("failed to get sender balance\t%s balance=%d", s, pb)
		}
		return
	}) {
		return
	}
	bal := pb.Uint64()
	log.Printf("Got sender balance\t%s balance=%d\n", s, bal)

	fee := new(big.Int).Mul(s.gasPrice, new(big.Int).SetUint64(config.gas))
	amt := fee.Mul(fee, new(big.Int).SetUint64(1000)).Uint64()
	if bal < amt {
		s.transition(senderSeedState)
		if !bo.do(ctx, func() error {
			err := s.requestSeed(ctx, amt-bal)
			if err != nil {
				return fmt.Errorf("failed to seed account\t%s", s)
			}
			return nil
		}) {
			return
		}
		log.Printf("Seeded account\t%s\n", s)
		s.transition(senderAssignState)
	}

	if !bo.do(ctx, func() error {
		s.recv = s.AccountStore.NextRecv(s.acct.Address, rand.Intn(10)+1)
		if len(s.recv) == 0 {
			return fmt.Errorf("failed to assig sender receivers\t%s receivers=%v", s, receivers(s.recv))
		}
		return nil
	}) {
		return
	}
	log.Printf("Assigned sender receivers\t%s receivers=%s\n", s, receivers(s.recv))
}

type receivers []common.Address

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

func (s *Sender) requestSeed(ctx context.Context, amount uint64) error {
	resp := make(chan error)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.SeedCh <- SeedReq{
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

func (s *Sender) Send(ctx context.Context, txs <-chan struct{}, done func()) {
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

	newAcct := time.After(time.Second * time.Duration(300+rand.Intn(60)))
	updateGas := time.After(time.Second * time.Duration(300+rand.Intn(60)))
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-newAcct:
			s.transition(senderAssignState)
			s.assignAcct(ctx)
			newAcct = time.After(time.Second * time.Duration(300+rand.Intn(60)))
			s.transition(senderSendState)
		case <-updateGas:
			s.transition(senderUpdateGasState)
			s.updateGasPrice(ctx)
			updateGas = time.After(time.Second * time.Duration(300+rand.Intn(60)))
			s.transition(senderSendState)
		case <-txs:
			s.send(ctx)
		}
	}
}

func (s *Sender) updateGasPrice(ctx context.Context) {
	bo := backOff{maxWait: 30 * time.Second, wait: 1 * time.Second}
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

	gasPrice := new(big.Int).Div(s.gasPrice, new(big.Int).SetInt64(rand.Int63n(5)+5))
	gasPrice.Add(s.gasPrice, gasPrice)

	tx := types.NewTransaction(s.nonce, recv, config.amount, config.gas, gasPrice, nil)
	t := time.Now()
	tx, err := s.AccountStore.SignTx(*s.acct, tx)
	if err != nil {
		log.Printf("Failed to sign tx\tsender=%d err=%q\n", s.Number, err)
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
		return
	}
	if ctx.Err() != nil {
		return
	}
	sendTxErrMeter.Mark(1)
	var print bool
	var wait time.Duration
	if msg := err.Error(); nonceErr(msg) {
		log.Printf("Failed to send - updating nonce\t%s err=%q\n", s, err)
		old := s.nonce
		bo := backOff{maxWait: 30 * time.Second, wait: 1 * time.Second}
		if !bo.doTimed(ctx, pendingNonceAtTimer, func() (err error) {
			s.nonce, err = s.Client.PendingNonceAt(ctx, s.acct.Address)
			if err != nil {
				err = fmt.Errorf("failed to get nonce\t%s err=%q", s, err)
			}
			return
		}) {
			return
		}
		log.Printf("Updated nonce\t%s old=%d new=%d\n", s, old, s.nonce)
		return
	} else if msg == "transaction pool limit reached" {
		print = true
		wait = 5 * time.Minute
	} else if knownTxErr(msg) || lowFundsErr(msg) {
		log.Printf("Abandoning account\t%s err=%s\n", s, msg)
		s.transition(senderAssignState)
		s.assignAcct(ctx)
		s.transition(senderSendState)
		return
	} else {
		print = true
		wait = 1 * time.Minute
	}
	if wait == 0 && (print || config.verbose) {
		log.Printf("Failed to send\t%s err=%q\n", s, err)
	}
	if wait != 0 {
		log.Printf("Pausing sender\t%s pause=%s err=%q\n", s, wait, err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
	return
}
