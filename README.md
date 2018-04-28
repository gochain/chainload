# ChainLoad

[![CircleCI](https://circleci.com/gh/gochain-io/chainload.svg?style=svg)](https://circleci.com/gh/gochain-io/chainload)

`chainload` is a GoChain/Ethereum blockchain load generator.

## How to use

By default, simply executing `chainload` will fire 1 transaction per
second at `http://localhost:8545` with chain id `1234`. Reports are
logged every 30s, with pprof and various metrics are available via expvar.

The target url(s), transaction rate, chain id, and more can be set via
flags:

```
chainload --help

Usage of chainload:
  -amount uint
    	tx amount (approximate) (default 10)
  -dur duration
    	duration to run - omit for unlimited
  -gas uint
    	gas (approximate) (default 200000)
  -id uint
    	id (default 1234)
  -pass string
    	passphrase to unlock accounts (default "#go@chain42")
  -pprof string
    	pprof addr (default ":6060")
  -senders int
    	total number of concurrent senders/accounts - defaults to 1/10 of tps
  -tps int
    	transactions per second (default 1)
  -urls string
    	csv of urls (default "http://localhost:8545")
  -v	verbose logging
```

Examples:

```
chainload -id 9876 -urls http://node1:8545,http://node2:8545 -tps 100 -senders 50 -dur 5m
```

```
chainload version
> chainload version: 0.0.1

```

## How it works

Accounts are managed locally under `keystore/`. Seeder accounts must
be pre-existing. One seeder is started per url, to continually re-claim
funds from other accounts, and to seed funds to senders. Senders may
reuse pre-existing accounts or create new ones. Senders continually send
txs to a set of receivers, while periodically cycling out the sender and
receiver addresses. The `gas` and `amount` of each transaction varies
randomly from the suggested approximate values.

## Problems

At high volume, the error `Too many open files` may occur. This system
limit can be inspected via `ulimit -n`, and temporarily raised
via `ulimit -n <new limit>`. It can be permanently set in
`/etc/security/limits.conf` by adding a line like:
```
root             soft    nofile          100000
```
