package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"time"

	"github.com/gochain-io/chainload"
)

var version string

func init() {
	if version == "" {
		version = "unknown"
	}
}

var config = chainload.Config{}

func init() {
	flag.Uint64Var(&config.Id, "id", 1234, "Id")
	flag.StringVar(&config.UrlsCSV, "urls", "http://localhost:8545", "csv of urls")
	flag.IntVar(&config.TPS, "tps", 1, "transactions per second")
	flag.IntVar(&config.Senders, "senders", 0, "total number of concurrent Senders/accounts - defaults to TPS")
	flag.DurationVar(&config.Cycle, "cycle", 5*time.Minute, "how often to Cycle a sender's account")
	flag.DurationVar(&config.Duration, "dur", 0, "duration to run - omit for unlimited")
	flag.StringVar(&config.Password, "pass", "#go@chain42", "passphrase to unlock accounts")
	flag.Uint64Var(&config.Gas, "gas", 200000, "Gas (approximate)")
	flag.Uint64Var(&config.Amount, "amount", 10, "tx Amount (approximate)")
	flag.StringVar(&config.PprofAddr, "pprof", ":6060", "pprof addr")
	flag.BoolVar(&config.Verbose, "v", false, "Verbose logging")
	flag.DurationVar(&config.Variable, "variable", 30*time.Second, "Variable transaction rate")
}

func main() {
	if fi, err := os.Stdin.Stat(); err != nil {
		log.Fatalf("Failed to check stdin: %v", err)
	} else if fi.Size() > 0 {
		log.Fatalf("Illegal input: non-empty stdin")
	}
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		if len(args) == 1 && args[0] == "version" {
			fmt.Fprintln(os.Stdout, "chainload version:", version)
			os.Exit(0)
		}
		log.Fatalf("Illegal extra arguments: %v", flag.Args())
	}

	cl, err := chainload.NewChainload(config)
	if err != nil {
		log.Fatalf("Failed to set up\terr=%q\n", err)
	}

	// pprof
	runtime.SetBlockProfileRate(1000000)
	runtime.SetMutexProfileFraction(1000000)
	server := &http.Server{Addr: config.PprofAddr}
	defer server.Close()
	go func() {
		log.Println("ListenAndServe stopped:", server.ListenAndServe())
	}()

	log.Println("Version:", version)
	log.Println("Starting execution:", &config)
	err = cl.Run()
	if err != nil {
		log.Fatalf("Failed\terr=%q\n", err)
	}
}
