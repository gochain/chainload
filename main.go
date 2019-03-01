package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gochain-io/gochain/v3/accounts/keystore"
	"github.com/gochain-io/gochain/v3/goclient"
	"github.com/pkg/errors"
)

type Config struct {
	id        uint64
	urlsCSV   string
	tps       int
	senders   int
	cycle     time.Duration
	dur       time.Duration
	pass      string
	gas       uint64
	amount    uint64
	pprofAddr string
	verbose   bool
	variable  time.Duration
}

func (c *Config) String() string {
	return fmt.Sprintf("id=%d urls=%q tps=%d senders=%d cycle=%s dur=%s pass=%q gas=%d amount=%d "+
		"pprof=%q verbose=%t variable=%s",
		config.id, config.urlsCSV, config.tps, config.senders, config.cycle, config.dur, config.pass, config.gas,
		config.amount, config.pprofAddr, config.verbose, config.variable)
}

var config = Config{}

func init() {
	flag.Uint64Var(&config.id, "id", 1234, "id")
	flag.StringVar(&config.urlsCSV, "urls", "http://localhost:8545", "csv of urls")
	flag.IntVar(&config.tps, "tps", 1, "transactions per second")
	flag.IntVar(&config.senders, "senders", 0, "total number of concurrent senders/accounts - defaults to tps")
	flag.DurationVar(&config.cycle, "cycle", 5*time.Minute, "how often to cycle a sender's account")
	flag.DurationVar(&config.dur, "dur", 0, "duration to run - omit for unlimited")
	flag.StringVar(&config.pass, "pass", "#go@chain42", "passphrase to unlock accounts")
	flag.Uint64Var(&config.gas, "gas", 200000, "gas (approximate)")
	flag.Uint64Var(&config.amount, "amount", 10, "tx amount (approximate)")
	flag.StringVar(&config.pprofAddr, "pprof", ":6060", "pprof addr")
	flag.BoolVar(&config.verbose, "v", false, "verbose logging")
	flag.DurationVar(&config.variable, "variable", 30*time.Second, "variable transaction rate")
}

func main() {
	nodes, err := setup()
	if err != nil {
		log.Fatalf("Failed to set up\terr=%q\n", err)
	}

	// pprof
	runtime.SetBlockProfileRate(1000000)
	runtime.SetMutexProfileFraction(1000000)
	go func() {
		log.Println(http.ListenAndServe(config.pprofAddr, nil))
	}()

	log.Println("Version:", Version)
	log.Println("Starting execution:", &config)
	err = run(nodes)
	if err != nil {
		log.Fatalf("Failed\terr=%q\n", err)
	}
}

func setup() ([]*Node, error) {
	if fi, err := os.Stdin.Stat(); err != nil {
		return nil, err
	} else if fi.Size() > 0 {
		return nil, errors.New("illegal input: non-empty stdin")
	}
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		if len(args) == 1 && args[0] == "version" {
			fmt.Fprintln(os.Stdout, "chainload version:", Version)
			os.Exit(0)
		}
		return nil, fmt.Errorf("illegal extra arguments: %v", flag.Args())
	}
	if config.tps < 1 {
		return nil, fmt.Errorf("illegal tps argument: %d", config.tps)
	}
	if config.senders < 1 {
		config.senders = config.tps
	}

	as := NewAccountStore(keystore.NewPlaintextKeyStore("keystore"), new(big.Int).SetUint64(config.id))
	urls := strings.Split(config.urlsCSV, ",")

	var nodes []*Node
	for i := range urls {
		url := urls[i]
		client, err := goclient.Dial(url)
		if err != nil {
			log.Printf("Failed to dial\turl=%s err=%q\n", url, err)
		} else {
			nodes = append(nodes, &Node{
				Number:       i,
				Client:       client,
				AccountStore: as,
				SeedCh:       make(chan SeedReq),
			})
		}
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("failed to dial all nodes\turls=%d", len(urls))
	}
	return nodes, nil
}

func run(nodes []*Node) error {
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
	for _, node := range nodes {
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
		return fmt.Errorf("failed to create any seeders\tcount=%d", len(nodes))
	}
	log.Printf("Started seeders\tcount=%d\n", seeders)

	sendBlock, err := waitBlocks(ctx, nodes[0].Client, 0)
	if err != nil {
		// Cancelled.
		return err
	}
	log.Printf("Starting senders\tcount=%d block=%d\n", config.senders, sendBlock)
	stats := NewReporter()
	if config.dur != 0 {
		t := time.AfterFunc(config.dur, func() {
			cancelFn()
		})
		defer t.Stop()
	}

	wg.Add(config.senders)
	txsIn := make(chan struct{}, config.tps*10)
	txsOut := txsIn

	if config.variable > 0 {
		// Spawn a goroutine to intercept released txs and sporadically delay them to vary the rate.
		txsOut = make(chan struct{}, config.tps)
		go func() {
			defer close(txsOut)
			next := time.Now()
			for tx := range txsIn {
				if time.Until(next) <= 0 {
					time.Sleep(randBetweenDur(0, config.variable))
					next = time.Now().Add(randBetweenDur(config.variable/2, config.variable))
				}
				txsOut <- tx
			}
		}()
	}

	// Individual sender tps limit is 10x ideal.
	tpsLimit := 10 * config.tps / config.senders
	if tpsLimit == 0 {
		tpsLimit = 1
	}

	for num := 0; num < config.senders; num++ {
		node := num % len(nodes)
		s := Sender{
			Number:    num,
			Node:      nodes[node],
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
	distribute(config.tps, batches)
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

	bigBlock, err := nodes[0].LatestBlockNumber(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		log.Printf("Failed to get block number\terr=%q\n", err)
	} else {
		log.Printf("Ran between blocks\tstart=%d end=%d\n", sendBlock, bigBlock)
	}
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
