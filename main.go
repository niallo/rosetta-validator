// Copyright 2020 Coinbase, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/coinbase/rosetta-validator/internal/logger"
	"github.com/coinbase/rosetta-validator/internal/reconciler"
	"github.com/coinbase/rosetta-validator/internal/storage"
	"github.com/coinbase/rosetta-validator/internal/syncer"

	"github.com/coinbase/rosetta-sdk-go/fetcher"
	rosetta "github.com/coinbase/rosetta-sdk-go/gen"

	"github.com/caarlos0/env"
	"golang.org/x/sync/errgroup"
)

type config struct {
	DataDir                string `env:"DATA_DIR,required"`
	ServerAddr             string `env:"SERVER_ADDR,required"`
	BlockConcurrency       uint64 `env:"BLOCK_CONCURRENCY,required"`
	TransactionConcurrency uint64 `env:"TRANSACTION_CONCURRENCY,required"`
	AccountConcurrency     int    `env:"ACCOUNT_CONCURRENCY,required"`
	LogTransactions        bool   `env:"LOG_TRANSACTIONS,required"`
	LogBenchmarks          bool   `env:"LOG_BENCHMARKS,required"`
}

func main() {
	ctx := context.Background()

	cfg := config{}
	if err := env.Parse(&cfg); err != nil {
		log.Fatal(err)
	}

	fetcher := fetcher.New(
		ctx,
		cfg.ServerAddr,
		"rosetta-validator",
		&http.Client{
			Timeout: 10 * time.Second,
		},
		cfg.BlockConcurrency,
		cfg.TransactionConcurrency,
	)

	networkResponse, err := fetcher.InitializeAsserter(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// TODO: sync and reconcile on subnetworks, if they exist.
	network := &rosetta.NetworkIdentifier{
		Network:    networkResponse.NetworkStatus.NetworkIdentifier.Network,
		Blockchain: networkResponse.NetworkStatus.NetworkIdentifier.Blockchain,
	}

	localStore, err := storage.NewBadgerStorage(ctx, cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}

	blockStorage := storage.NewBlockStorage(ctx, localStore)
	logger := logger.NewLogger(cfg.DataDir, cfg.LogTransactions, cfg.LogBenchmarks)

	g, ctx := errgroup.WithContext(ctx)

	var r *reconciler.Reconciler
	if reconciler.ShouldReconcile(networkResponse) {
		log.Printf("Balance reconciliation enabled\n")

		r = reconciler.New(
			ctx,
			network,
			blockStorage,
			fetcher,
			logger,
			cfg.AccountConcurrency,
		)

		g.Go(func() error {
			return r.Reconcile(ctx)
		})
	}

	syncer := syncer.New(
		ctx,
		network,
		blockStorage,
		fetcher,
		logger,
		r,
	)
	g.Go(func() error {
		return syncer.Sync(ctx)
	})

	err = g.Wait()
	if err != nil {
		log.Fatal(err)
	}
}
