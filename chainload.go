package chainload

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gochain/gochain/v3/accounts/keystore"
	"github.com/gochain/gochain/v3/goclient"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	Id        uint64
	UrlsCSV   string
	TPS       int
	Senders   int
	Cycle     time.Duration
	Duration  time.Duration
	Password  string
	Gas       uint64
	Amount    uint64
	PprofAddr string
	Variable  time.Duration
}

func (c *Config) MarshalLogObject(oe zapcore.ObjectEncoder) error {
	oe.AddUint64("id", c.Id)
	oe.AddString("urls", c.UrlsCSV)
	oe.AddInt("tps", c.TPS)
	oe.AddInt("senders", c.Senders)
	oe.AddDuration("cycle", c.Cycle)
	oe.AddDuration("duration", c.Duration)
	// don't log password
	oe.AddUint64("gas", c.Gas)
	oe.AddUint64("amount", c.Amount)
	oe.AddString("pprofAddr", c.PprofAddr)
	oe.AddDuration("variable", c.Variable)
	return nil
}

type Chainload struct {
	config *Config
	lgr    *zap.Logger
	nodes  []*Node
}

func (config *Config) NewChainload(lgr *zap.Logger) (*Chainload, error) {
	if config.TPS < 1 {
		return nil, fmt.Errorf("illegal TPS argument: %d", config.TPS)
	}
	if config.Senders < 1 {
		config.Senders = config.TPS
	}

	lgr.Info("Opening keystore...")
	start := time.Now()
	as := NewAccountStore(keystore.NewPlaintextKeyStore("keystore"), new(big.Int).SetUint64(config.Id), config.Password)
	lgr.Info("Keystore opened", zap.Duration("duration", time.Since(start)))
	urls := strings.Split(config.UrlsCSV, ",")

	var nodes []*Node
	for i := range urls {
		url := urls[i]
		client, err := goclient.Dial(url)
		if err != nil {
			lgr.Warn("Failed to dial", zap.String("url", url), zap.Error(err))
			continue
		}
		chainID, err := client.ChainID(context.Background())
		if err != nil {
			lgr.Warn("Failed to check chain ID", zap.String("url", url), zap.Error(err))
			continue
		} else if chainID == nil || config.Id != chainID.Uint64() {
			lgr.Warn("Wrong chain ID", zap.Uint64("configID", config.Id),
				zap.Uint64("nodeID", chainID.Uint64()), zap.String("url", url), zap.Error(err))
			continue
		}
		nodes = append(nodes, &Node{
			lgr:          lgr.With(zap.Int("node", i), zap.String("url", url)),
			Number:       i,
			gas:          config.Gas,
			Client:       client,
			AccountStore: as,
			SeedCh:       make(chan SeedReq),
		})
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("failed to dial all %d urls: %v", len(urls), urls)
	}
	return &Chainload{config: config, lgr: lgr, nodes: nodes}, nil
}

func (c *Chainload) Run() error {
	ctx, cancelFn := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			c.lgr.Info("Signal received. Stopping...", zap.String("signal", sig.String()))
			cancelFn()
		}
	}()

	var wg sync.WaitGroup
	var seeders int
	for _, node := range c.nodes {
		acct, err := node.NextSeed()
		if err != nil {
			c.lgr.Warn("Failed to get seeder account", zap.Error(err))
		}
		if err != nil || acct == nil {
			acct, err = node.New(ctx)
			if err != nil {
				c.lgr.Warn("Failed to create new seeder account", zap.Error(err))
				continue
			}
		}
		s := &Seeder{
			Node: node,
			acct: acct,
		}
		wg.Add(1)
		seeders++
		go s.Run(ctx, wg.Done)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if seeders == 0 {
		return fmt.Errorf("failed to create any seeders for %d nodes", len(c.nodes))
	}
	c.lgr.Info("Started seeders", zap.Int("count", seeders))

	start := time.Now()
	c.lgr.Info("Starting senders", zap.Int("count", c.config.Senders))
	stats := NewReporter()
	if c.config.Duration != 0 {
		t := time.AfterFunc(c.config.Duration, func() {
			cancelFn()
		})
		defer t.Stop()
	}

	wg.Add(c.config.Senders)
	txsIn := make(chan struct{}, c.config.TPS*10)
	txsOut := txsIn

	if c.config.Variable > 0 {
		// Spawn a goroutine to intercept released txs and sporadically delay them to vary the rate.
		txsOut = make(chan struct{}, c.config.TPS)
		go func() {
			defer close(txsOut)
			nextPause := time.Now()
			for {
				select {
				case <-ctx.Done():
					return
				case tx := <-txsIn:
					if time.Until(nextPause) <= 0 {
						wait := randBetweenDur(0, c.config.Variable)
						select {
						case <-ctx.Done():
							return
						case <-time.After(wait):
						}
						nextPause = time.Now().Add(randBetweenDur(c.config.Variable/2, c.config.Variable))
					}
					txsOut <- tx
				}
			}
		}()
	}

	// Individual sender TPS limit is 10x ideal.
	tpsLimit := 10 * c.config.TPS / c.config.Senders
	if tpsLimit == 0 {
		tpsLimit = 1
	}

	for num := 0; num < c.config.Senders; num++ {
		node := num % len(c.nodes)
		s := Sender{
			Number:    num,
			amount:    c.config.Amount,
			cycle:     c.config.Cycle,
			Node:      c.nodes[node],
			RateLimit: time.Second / time.Duration(tpsLimit),
		}
		go s.Send(ctx, txsOut, wg.Done)
	}

	// 1/10 second batches, with reports every 30s.
	const batchCount = 10
	batch := time.NewTicker(time.Second / batchCount)
	report := time.NewTicker(30 * time.Second)
	defer batch.Stop()
	defer report.Stop()

	batches := make([]int, batchCount)
	distribute(c.config.TPS, batches)
	rand.Shuffle(len(batches), func(i, j int) {
		batches[i], batches[j] = batches[j], batches[i]
	})

	var reports Reports
	var cnt int
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-report.C:
			s := reports.Add(stats.Report())
			c.lgr.Info("Status", zap.Object("status", s))
		case <-batch.C:
			batchSize := batches[cnt%len(batches)]
			for i := 0; i < batchSize; i++ {
				txsIn <- struct{}{}
			}
			cnt++
		}
	}
	close(txsIn)
	cancelFn()
	wg.Wait()

	s := reports.Add(stats.Report())
	end := time.Now()
	c.lgr.Info("Final Status", zap.Object("status", s), zap.Time("start", start), zap.Time("end", end))
	return nil
}

func waitBlocks(ctx context.Context, lgr *zap.Logger, client *goclient.Client, blocks uint64) (uint64, error) {
	var first *uint64
	for {
		t := time.Now()
		big, err := client.LatestBlockNumber(ctx)
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		latestBlockNumberTimer.UpdateSince(t)
		if err != nil {
			lgr.Warn("Failed to get latest block number", zap.Error(err))
		} else {
			current := big.Uint64()
			if first == nil {
				first = &current
			}
			if current >= *first+blocks {
				return current, nil
			}
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}
