package chainload

import (
	"context"
	"math/big"
	"time"

	"github.com/gochain-io/gochain/v3/accounts"
	"github.com/gochain-io/gochain/v3/common"
	"github.com/gochain-io/gochain/v3/core/types"
	"github.com/gochain-io/gochain/v3/goclient"
)

type Node struct {
	Number  int
	gas     uint64
	verbose bool
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

	gas := randBetween(n.gas, 2*n.gas)
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
