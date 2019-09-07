package chainload

import (
	"context"
	"fmt"
	"log"
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
	Verbose   bool
	Variable  time.Duration
}

func (c *Config) String() string {
	return fmt.Sprintf("Id=%d urls=%q TPS=%d Senders=%d Cycle=%s Duration=%s Password=%q Gas=%d Amount=%d "+
		"pprof=%q Verbose=%t Variable=%s",
		c.Id, c.UrlsCSV, c.TPS, c.Senders, c.Cycle, c.Duration, c.Password, c.Gas,
		c.Amount, c.PprofAddr, c.Verbose, c.Variable)
}

type Chainload struct {
	config Config
	nodes  []*Node
}

func NewChainload(config Config) (*Chainload, error) {
	if config.TPS < 1 {
		return nil, fmt.Errorf("illegal TPS argument: %d", config.TPS)
	}
	if config.Senders < 1 {
		config.Senders = config.TPS
	}

	as := NewAccountStore(keystore.NewPlaintextKeyStore("keystore"), new(big.Int).SetUint64(config.Id), config.Password)
	urls := strings.Split(config.UrlsCSV, ",")

	var nodes []*Node
	for i := range urls {
		url := urls[i]
		client, err := goclient.Dial(url)
		if err != nil {
			log.Printf("Failed to dial\turl=%s err=%q\n", url, err)
		} else {
			nodes = append(nodes, &Node{
				Number:       i,
				gas:          config.Gas,
				verbose:      config.Verbose,
				Client:       client,
				AccountStore: as,
				SeedCh:       make(chan SeedReq),
			})
		}
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("failed to dial all nodes\turls=%d", len(urls))
	}
	return &Chainload{config: config, nodes: nodes}, nil
}

func (c *Chainload) Run() error {
	ctx, cancelFn := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sigCh {
			cancelFn()
		}
	}()

	var wg sync.WaitGroup
	var seeders int
	for _, node := range c.nodes {
		acct, err := node.NextSeed()
		if err != nil {
			log.Printf("Failed to get seeder account\terr=%q\n", err)
		}
		if err != nil || acct == nil {
			acct, err = node.New(ctx)
			if err != nil {
				log.Printf("Failed to create new seeder account\terr=%q\n", err)
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
		return fmt.Errorf("failed to create any seeders\tcount=%d", len(c.nodes))
	}
	log.Printf("Started seeders\tcount=%d\n", seeders)

	start := time.Now()
	log.Printf("Starting Senders\tcount=%d start=%s\n", c.config.Senders, start)
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
			next := time.Now()
			for tx := range txsIn {
				if time.Until(next) <= 0 {
					time.Sleep(randBetweenDur(0, c.config.Variable))
					next = time.Now().Add(randBetweenDur(c.config.Variable/2, c.config.Variable))
				}
				txsOut <- tx
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
			log.Println("Status:")
			log.Println(s)
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
	log.Println("Final Status:")
	log.Println(s)
	end := time.Now()
	dur := end.Sub(start)
	log.Printf("Ran for %s\tstart=%s end=%s\n", dur.Round(time.Second), start.Format(time.RFC3339), end.Format(time.RFC3339))
	return nil
}

func waitBlocks(ctx context.Context, client *goclient.Client, blocks uint64) (uint64, error) {
	var first *uint64
	for {
		t := time.Now()
		big, err := client.LatestBlockNumber(ctx)
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		latestBlockNumberTimer.UpdateSince(t)
		if err != nil {
			log.Printf("Failed to get block number\terr=%q\n", err)
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
