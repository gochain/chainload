package chainload

import (
	"context"
	"math/big"
	"time"

	"github.com/gochain/gochain/v3/accounts"
	"github.com/gochain/gochain/v3/common"
	"github.com/gochain/gochain/v3/core/types"
	"github.com/gochain/gochain/v3/goclient"
	"go.uber.org/zap"
)

type Node struct {
	lgr    *zap.Logger
	Number int
	gas    uint64
	*goclient.Client
	*AccountStore
	SeedCh chan SeedReq
}

func (n *Node) refund(ctx context.Context, acct accounts.Account, nonce uint64, seed common.Address) (*big.Int, error) {
	t := time.Now()
	bal, err := n.PendingBalanceAt(ctx, acct.Address)
	if err != nil {
		return nil, err
	}
	pendingBalanceAtTimer.UpdateSince(t)

	t = time.Now()
	gasPrice, err := n.SuggestGasPrice(ctx)
	if err != nil {
		return nil, err
	}
	suggestGasPriceTimer.UpdateSince(t)

	gas := randBetween(n.gas, 2*n.gas)
	var amount big.Int
	amount.Mul(new(big.Int).SetUint64(gas), gasPrice)
	amount.Sub(bal, &amount)
	if amount.Cmp(new(big.Int)) < 1 {
		return nil, nil
	}
	tx := types.NewTransaction(nonce, seed, &amount, gas, gasPrice, nil)

	t = time.Now()
	tx, err = n.SignTx(acct, tx)
	if err != nil {
		return nil, err
	}
	signTxTimer.UpdateSince(t)

	t = time.Now()
	err = n.SendTransaction(ctx, tx)
	if err != nil {
		sendTxErrMeter.Mark(1)
		return nil, err
	}
	sendTxTimer.UpdateSince(t)

	return &amount, nil
}
