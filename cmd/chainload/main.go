package main

import (
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"time"

	"github.com/blendle/zapdriver"
	"github.com/gochain-io/chainload"
	"go.uber.org/zap"
)

var version string

func init() {
	if version == "" {
		version = "unknown"
	}
}

var (
	config chainload.Config
	logCfg zap.Config
)

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
	flag.DurationVar(&config.Variable, "variable", 30*time.Second, "Variable transaction rate")

	humanLogs := flag.Bool("human", true, "Human readable logs")
	flag.Parse()
	if *humanLogs {
		logCfg = zap.NewDevelopmentConfig()
	} else {
		logCfg = zapdriver.NewProductionConfig()
	}
}

func main() {
	start := time.Now()
	lgr, err := logCfg.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}

	if fi, err := os.Stdin.Stat(); err != nil {
		lgr.Fatal("Failed to check stdin", zap.Error(err))
	} else if fi.Size() > 0 {
		lgr.Fatal("Illegal input: non-empty stdin")
	}
	if args := flag.Args(); len(args) > 0 {
		if len(args) == 1 && args[0] == "version" {
			fmt.Fprintln(os.Stdout, "chainload version:", version)
			os.Exit(0)
		}
		lgr.Fatal("Illegal extra arguments", zap.Strings("args", flag.Args()))
	}

	cl, err := config.NewChainload(lgr)
	if err != nil {
		lgr.Fatal("Failed to create Chainload", zap.Error(err))
	}

	// pprof
	runtime.SetBlockProfileRate(1000000)
	runtime.SetMutexProfileFraction(1000000)
	server := &http.Server{Addr: config.PprofAddr}
	defer server.Close()
	go func() {
		err := server.ListenAndServe()
		if err != nil {
			lgr.Error("ListenAndServe stopped", zap.Error(err))
			return
		}
		lgr.Info("ListenAndServe stopped")
	}()

	lgr.Info("Staring Chainload", zap.String("version", version), zap.Object("config", &config))
	err = cl.Run()
	if err != nil {
		lgr.Fatal("Fatal error", zap.Error(err), zap.Duration("runtime", time.Since(start)))
	}
	lgr.Info("Shutting down", zap.Duration("runtime", time.Since(start)))
}
