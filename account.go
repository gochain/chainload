package chainload

import (
	"context"
	"math/big"
	"math/rand"
	"sync"

	"github.com/gochain-io/gochain/v3/accounts"
	"github.com/gochain-io/gochain/v3/accounts/keystore"
	"github.com/gochain-io/gochain/v3/common"
	"github.com/gochain-io/gochain/v3/core/types"
)

type AccountStore struct {
	ks      *keystore.KeyStore
	chainID *big.Int
	ksAccts []accounts.Account
	pass    string

	acctsMu    sync.RWMutex
	nextIdx    int
	ksAcctsSet map[common.Address]struct{}
	addrs      []common.Address
	pools      map[int]map[common.Address]acctNonce
	seeds      map[common.Address]struct{}
}

type acctNonce struct {
	*accounts.Account
	nonce uint64
}

func NewAccountStore(ks *keystore.KeyStore, chainID *big.Int, pass string) *AccountStore {
	return &AccountStore{
		ks:         ks,
		ksAccts:    ks.Accounts(),
		chainID:    chainID,
		ksAcctsSet: make(map[common.Address]struct{}),
		pools:      make(map[int]map[common.Address]acctNonce),
		seeds:      make(map[common.Address]struct{}),
	}
}

func (a *AccountStore) SignTx(acct accounts.Account, tx *types.Transaction) (*types.Transaction, error) {
	return a.ks.SignTx(acct, tx, a.chainID)
}

func (a *AccountStore) NextRecv(send common.Address, n int) []common.Address {
	a.acctsMu.RLock()
	defer a.acctsMu.RUnlock()

	var addrs []common.Address
	start := rand.Intn(len(a.addrs))
	for i := 0; len(addrs) < n && i < len(a.addrs); i++ {
		addr := a.addrs[(start+i)%len(a.addrs)]
		if addr != send {
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

func (a *AccountStore) Next(ctx context.Context, node int) (acct *accounts.Account, nonce uint64, err error) {
	a.acctsMu.Lock()
	defer a.acctsMu.Unlock()
	if len(a.pools) > 0 && rand.Intn(2) == 0 {
		pool := a.pools[node]
		if pool != nil {
			for addr, an := range pool {
				delete(pool, addr)
				return an.Account, an.nonce, nil
			}
		}
	}

	acct = a.nextAcct()
	if acct == nil {
		return
	}
	err = a.ks.Unlock(*acct, a.pass)
	return
}

func (a *AccountStore) New(ctx context.Context) (*accounts.Account, error) {
	acct, err := a.ks.NewAccount(a.pass)
	if err != nil {
		return nil, err
	}

	a.acctsMu.Lock()
	a.addrs = append(a.addrs, acct.Address)
	a.acctsMu.Unlock()
	return &acct, a.ks.Unlock(acct, a.pass)
}

func (a *AccountStore) Return(acct *accounts.Account, node int, nonce uint64) {
	a.acctsMu.Lock()
	pool := a.pools[node]
	if pool == nil {
		pool = make(map[common.Address]acctNonce)
		a.pools[node] = pool
	}
	pool[acct.Address] = acctNonce{Account: acct, nonce: nonce}
	a.acctsMu.Unlock()
}

func (a *AccountStore) RandSeed() *common.Address {
	a.acctsMu.RLock()
	defer a.acctsMu.RUnlock()
	for addr := range a.seeds {
		return &addr
	}
	return nil
}

// NextSeed returns the next available account from the keystore.
func (a *AccountStore) NextSeed() (*accounts.Account, error) {
	a.acctsMu.Lock()
	defer a.acctsMu.Unlock()
	acct := a.nextAcct()
	if acct == nil {
		return nil, nil
	}
	a.seeds[acct.Address] = struct{}{}
	return acct, a.ks.Unlock(*acct, a.pass)
}

// nextAcct returns the next available account from the keystore, or nil if none
// are available.
func (a *AccountStore) nextAcct() *accounts.Account {
	for {
		if a.nextIdx >= len(a.ksAccts) {
			return nil
		}
		i := a.nextIdx
		a.nextIdx++
		acct := a.ksAccts[i]
		if acct == (accounts.Account{}) {
			continue
		}
		if _, ok := a.ksAcctsSet[acct.Address]; ok {
			continue
		}
		a.ksAcctsSet[acct.Address] = struct{}{}
		a.addrs = append(a.addrs, acct.Address)
		return &acct
	}
}
