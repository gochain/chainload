package main

import (
	"context"
	"math/big"
	"time"

	"github.com/gochain-io/gochain/accounts"
	"github.com/gochain-io/gochain/common"
	"github.com/gochain-io/gochain/core/types"
	"github.com/gochain-io/gochain/goclient"
)

type Node struct {
	Number int
	*goclient.Client
	*AccountStore
	SeedCh chan SeedReq
}

func (n *Node) refund(ctx context.Context, acct accounts.Account, nonce uint64, seed common.Address) (uint64, error) {
	t := time.Now()
	bal, err := n.PendingBalanceAt(ctx, acct.Address)
	if err != nil {
		return 0, err
	}
	pendingBalanceAtTimer.UpdateSince(t)

	t = time.Now()
	gasPrice, err := n.SuggestGasPrice(ctx)
	if err != nil {
		return 0, err
	}
	suggestGasPriceTimer.UpdateSince(t)

	gas := randBetween(config.gas, 2*config.gas)
	var amount big.Int
	amount.Mul(new(big.Int).SetUint64(gas), gasPrice)
	amount.Sub(bal, &amount)
	if amount.Cmp(new(big.Int)) < 1 {
		return 0, nil
	}
	tx := types.NewTransaction(nonce, seed, &amount, gas, gasPrice, nil)

	t = time.Now()
	tx, err = n.SignTx(acct, tx)
	if err != nil {
		return 0, err
	}
	signTxTimer.UpdateSince(t)

	t = time.Now()
	err = n.SendTransaction(ctx, tx)
	if err != nil {
		sendTxErrMeter.Mark(1)
		return 0, err
	}
	sendTxTimer.UpdateSince(t)

	return amount.Uint64(), nil
}
