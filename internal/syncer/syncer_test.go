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

package syncer

import (
	"context"
	"fmt"
	"testing"

	"github.com/coinbase/rosetta-validator/internal/logger"
	"github.com/coinbase/rosetta-validator/internal/reconciler"
	"github.com/coinbase/rosetta-validator/internal/storage"

	"github.com/coinbase/rosetta-sdk-go/asserter"
	"github.com/coinbase/rosetta-sdk-go/fetcher"
	rosetta "github.com/coinbase/rosetta-sdk-go/gen"

	"github.com/stretchr/testify/assert"
)

var (
	currency = &rosetta.Currency{
		Symbol:   "Blah",
		Decimals: 2,
	}

	recipient = &rosetta.AccountIdentifier{
		Address: "acct1",
	}

	recipientAmount = &rosetta.Amount{
		Value:    "100",
		Currency: currency,
	}

	recipientOperation = &rosetta.Operation{
		OperationIdentifier: &rosetta.OperationIdentifier{
			Index: 0,
		},
		Type:    "Transfer",
		Status:  "Success",
		Account: recipient,
		Amount:  recipientAmount,
	}

	recipientFailureOperation = &rosetta.Operation{
		OperationIdentifier: &rosetta.OperationIdentifier{
			Index: 1,
		},
		Type:    "Transfer",
		Status:  "Failure",
		Account: recipient,
		Amount:  recipientAmount,
	}

	recipientTransaction = &rosetta.Transaction{
		TransactionIdentifier: &rosetta.TransactionIdentifier{
			Hash: "tx1",
		},
		Operations: []*rosetta.Operation{
			recipientOperation,
			recipientFailureOperation,
		},
	}

	sender = &rosetta.AccountIdentifier{
		Address: "acct2",
	}

	senderAmount = &rosetta.Amount{
		Value:    "-100",
		Currency: currency,
	}

	senderOperation = &rosetta.Operation{
		OperationIdentifier: &rosetta.OperationIdentifier{
			Index: 0,
		},
		Type:    "Transfer",
		Status:  "Success",
		Account: sender,
		Amount:  senderAmount,
	}

	senderTransaction = &rosetta.Transaction{
		TransactionIdentifier: &rosetta.TransactionIdentifier{
			Hash: "tx2",
		},
		Operations: []*rosetta.Operation{
			senderOperation,
		},
	}

	blockSequenceNoReorg = []*rosetta.Block{
		&rosetta.Block{ // genesis
			BlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "0",
				Index: 0,
			},
			ParentBlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "0",
				Index: 0,
			},
		},
		&rosetta.Block{
			BlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "1",
				Index: 1,
			},
			ParentBlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "0",
				Index: 0,
			},
		},
		&rosetta.Block{
			BlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "2",
				Index: 2,
			},
			ParentBlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "1",
				Index: 1,
			},
			Transactions: []*rosetta.Transaction{
				recipientTransaction,
			},
		},
		&rosetta.Block{
			BlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "3",
				Index: 3,
			},
			ParentBlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "2",
				Index: 2,
			},
			Transactions: []*rosetta.Transaction{
				senderTransaction,
			},
		},
	}

	blockSequenceReorg = []*rosetta.Block{
		&rosetta.Block{ // genesis
			BlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "0",
				Index: 0,
			},
			ParentBlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "0",
				Index: 0,
			},
		},
		&rosetta.Block{
			BlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "1",
				Index: 1,
			},
			ParentBlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "0",
				Index: 0,
			},
			Transactions: []*rosetta.Transaction{
				recipientTransaction,
			},
		},
		&rosetta.Block{
			BlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "2",
				Index: 2,
			},
			ParentBlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "1a",
				Index: 1,
			},
		},
		&rosetta.Block{
			BlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "1a",
				Index: 1,
			},
			ParentBlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "0",
				Index: 0,
			},
		},
		&rosetta.Block{
			BlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "3",
				Index: 3,
			},
			ParentBlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "2",
				Index: 2,
			},
		},
		&rosetta.Block{ // invalid block
			BlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "5",
				Index: 5,
			},
			ParentBlockIdentifier: &rosetta.BlockIdentifier{
				Hash:  "4",
				Index: 4,
			},
		},
	}

	operationStatuses = []*rosetta.OperationStatus{
		&rosetta.OperationStatus{
			Status:     "Success",
			Successful: true,
		},
		&rosetta.OperationStatus{
			Status:     "Failure",
			Successful: false,
		},
	}

	networkStatusResponse = &rosetta.NetworkStatusResponse{
		NetworkStatus: &rosetta.NetworkStatus{
			NetworkInformation: &rosetta.NetworkInformation{
				GenesisBlockIdentifier: &rosetta.BlockIdentifier{
					Index: 0,
				},
			},
		},
		Options: &rosetta.Options{
			OperationStatuses: operationStatuses,
		},
	}
)

func TestNoReorgProcessBlock(t *testing.T) {
	ctx := context.Background()

	newDir, err := storage.CreateTempDir()
	assert.NoError(t, err)
	defer storage.RemoveTempDir(*newDir)

	database, err := storage.NewBadgerStorage(ctx, *newDir)
	assert.NoError(t, err)
	defer database.Close(ctx)

	blockStorage := storage.NewBlockStorage(ctx, database)
	logger := logger.NewLogger(*newDir, false, false)
	fetcher := &fetcher.Fetcher{
		Asserter: asserter.New(ctx, networkStatusResponse),
	}
	rec := reconciler.New(ctx, nil, blockStorage, fetcher, logger, 1)
	syncer := New(ctx, nil, blockStorage, fetcher, logger, rec)
	currIndex := int64(0)

	t.Run("No block exists", func(t *testing.T) {
		modifiedAccounts, newIndex, err := syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceNoReorg[0],
		)
		currIndex = newIndex
		assert.Equal(t, int64(1), currIndex)
		assert.Equal(t, 0, len(modifiedAccounts))
		assert.NoError(t, err)

		tx := syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err := syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		tx.Discard(ctx)
		assert.Equal(t, blockSequenceNoReorg[0].BlockIdentifier, head)
		assert.NoError(t, err)
	})

	t.Run("Block exists, no reorg", func(t *testing.T) {
		modifiedAccounts, newIndex, err := syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceNoReorg[1],
		)
		currIndex = newIndex
		assert.Equal(t, int64(2), currIndex)
		assert.Equal(t, 0, len(modifiedAccounts))
		assert.NoError(t, err)

		tx := syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err := syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		tx.Discard(ctx)
		assert.Equal(t, blockSequenceNoReorg[1].BlockIdentifier, head)
		assert.NoError(t, err)
	})

	t.Run("Block with transaction", func(t *testing.T) {
		modifiedAccounts, newIndex, err := syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceNoReorg[2],
		)
		currIndex = newIndex
		assert.Equal(t, int64(3), currIndex)
		assert.Equal(t, []*reconciler.AccountAndCurrency{
			&reconciler.AccountAndCurrency{
				Account: &rosetta.AccountIdentifier{
					Address: "acct1",
				},
				Currency: currency,
			},
		}, modifiedAccounts)
		assert.NoError(t, err)

		tx := syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err := syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		assert.Equal(t, blockSequenceNoReorg[2].BlockIdentifier, head)
		assert.NoError(t, err)

		amounts, block, err := syncer.storage.GetBalance(ctx, tx, recipient)
		tx.Discard(ctx)

		// Ensure amount only increases by successful operation
		assert.Equal(t, map[string]*rosetta.Amount{
			storage.GetCurrencyKey(currency): recipientAmount,
		}, amounts)
		assert.Equal(t, blockSequenceNoReorg[2].BlockIdentifier, block)
		assert.NoError(t, err)
	})

	t.Run("Block with invalid transaction", func(t *testing.T) {
		modifiedAccounts, newIndex, err := syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceNoReorg[3],
		)
		currIndex = newIndex
		assert.Equal(t, int64(3), currIndex)
		assert.Equal(t, 0, len(modifiedAccounts))
		assert.Contains(t, err.Error(), storage.ErrNegativeBalance.Error())

		tx := syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err := syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		assert.Equal(t, blockSequenceNoReorg[2].BlockIdentifier, head)
		assert.NoError(t, err)

		amounts, block, err := syncer.storage.GetBalance(ctx, tx, sender)
		tx.Discard(ctx)
		assert.Nil(t, amounts)
		assert.Nil(t, block)
		assert.EqualError(t, err, fmt.Errorf(
			"%w %+v",
			storage.ErrAccountNotFound,
			sender,
		).Error())
	})
}

func TestReorgProcessBlock(t *testing.T) {
	ctx := context.Background()

	newDir, err := storage.CreateTempDir()
	assert.NoError(t, err)
	defer storage.RemoveTempDir(*newDir)

	database, err := storage.NewBadgerStorage(ctx, *newDir)
	assert.NoError(t, err)
	defer database.Close(ctx)

	blockStorage := storage.NewBlockStorage(ctx, database)
	logger := logger.NewLogger(*newDir, false, false)
	fetcher := &fetcher.Fetcher{
		Asserter: asserter.New(ctx, networkStatusResponse),
	}
	rec := reconciler.New(ctx, nil, blockStorage, fetcher, logger, 1)
	syncer := New(ctx, nil, blockStorage, fetcher, logger, rec)
	currIndex := int64(0)

	t.Run("No block exists", func(t *testing.T) {
		modifiedAccounts, newIndex, err := syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceReorg[0],
		)
		currIndex = newIndex
		assert.Equal(t, int64(1), currIndex)
		assert.Equal(t, 0, len(modifiedAccounts))
		assert.NoError(t, err)

		tx := syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err := syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		tx.Discard(ctx)
		assert.Equal(t, blockSequenceReorg[0].BlockIdentifier, head)
		assert.NoError(t, err)
	})

	t.Run("Block exists, no reorg", func(t *testing.T) {
		modifiedAccounts, newIndex, err := syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceReorg[1],
		)
		currIndex = newIndex
		assert.Equal(t, int64(2), currIndex)
		assert.Equal(t, []*reconciler.AccountAndCurrency{
			&reconciler.AccountAndCurrency{
				Account: &rosetta.AccountIdentifier{
					Address: "acct1",
				},
				Currency: currency,
			},
		}, modifiedAccounts)
		assert.NoError(t, err)

		tx := syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err := syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		assert.Equal(t, blockSequenceReorg[1].BlockIdentifier, head)
		assert.NoError(t, err)

		amounts, block, err := syncer.storage.GetBalance(ctx, tx, recipient)
		tx.Discard(ctx)
		assert.Equal(t, map[string]*rosetta.Amount{
			storage.GetCurrencyKey(currency): recipientAmount,
		}, amounts)
		assert.Equal(t, blockSequenceReorg[1].BlockIdentifier, block)
		assert.NoError(t, err)
	})

	t.Run("Orphan block", func(t *testing.T) {
		// Orphan block
		modifiedAccounts, newIndex, err := syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceReorg[2],
		)
		currIndex = newIndex
		assert.Equal(t, int64(1), currIndex)
		assert.Equal(t, []*reconciler.AccountAndCurrency{
			&reconciler.AccountAndCurrency{
				Account: &rosetta.AccountIdentifier{
					Address: "acct1",
				},
				Currency: currency,
			},
		}, modifiedAccounts)
		assert.NoError(t, err)

		// Assert head is back to genesis
		tx := syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err := syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		assert.Equal(t, blockSequenceReorg[0].BlockIdentifier, head)
		assert.NoError(t, err)

		// Assert that balance change was reverted
		// only by the successful operation
		zeroAmount := map[string]*rosetta.Amount{
			storage.GetCurrencyKey(currency): &rosetta.Amount{
				Value:    "0",
				Currency: currency,
			},
		}
		amounts, block, err := syncer.storage.GetBalance(ctx, tx, recipient)
		assert.Equal(t, zeroAmount, amounts)
		assert.Equal(t, blockSequenceReorg[0].BlockIdentifier, block)
		assert.NoError(t, err)

		// Assert block is gone
		orphanBlock, err := syncer.storage.GetBlock(ctx, tx, blockSequenceReorg[1].BlockIdentifier)
		assert.Nil(t, orphanBlock)
		assert.EqualError(t, err, fmt.Errorf(
			"%w %+v",
			storage.ErrBlockNotFound,
			blockSequenceReorg[1].BlockIdentifier,
		).Error())
		tx.Discard(ctx)

		// Process new block
		modifiedAccounts, currIndex, err = syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceReorg[3],
		)
		assert.Equal(t, int64(2), currIndex)
		assert.Equal(t, 0, len(modifiedAccounts))
		assert.NoError(t, err)

		tx = syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err = syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		tx.Discard(ctx)
		assert.Equal(t, blockSequenceReorg[3].BlockIdentifier, head)
		assert.NoError(t, err)

		modifiedAccounts, currIndex, err = syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceReorg[2],
		)
		assert.Equal(t, int64(3), currIndex)
		assert.Equal(t, 0, len(modifiedAccounts))
		assert.NoError(t, err)

		tx = syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err = syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		assert.Equal(t, blockSequenceReorg[2].BlockIdentifier, head)
		assert.NoError(t, err)

		amounts, block, err = syncer.storage.GetBalance(ctx, tx, recipient)
		tx.Discard(ctx)
		assert.Equal(t, zeroAmount, amounts)
		assert.Equal(t, blockSequenceReorg[0].BlockIdentifier, block)
		assert.NoError(t, err)

		modifiedAccounts, currIndex, err = syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceReorg[4],
		)
		assert.Equal(t, int64(4), currIndex)
		assert.Equal(t, 0, len(modifiedAccounts))
		assert.NoError(t, err)

		tx = syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err = syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		tx.Discard(ctx)
		assert.Equal(t, blockSequenceReorg[4].BlockIdentifier, head)
		assert.NoError(t, err)
	})

	t.Run("Out of order block", func(t *testing.T) {
		modifiedAccounts, newIndex, err := syncer.ProcessBlock(
			ctx,
			currIndex,
			blockSequenceReorg[5],
		)
		currIndex = newIndex
		assert.Equal(t, int64(4), currIndex)
		assert.Equal(t, 0, len(modifiedAccounts))
		assert.EqualError(t, err, "Got block 5 instead of 4")

		tx := syncer.storage.NewDatabaseTransaction(ctx, false)
		head, err := syncer.storage.GetHeadBlockIdentifier(ctx, tx)
		tx.Discard(ctx)
		assert.Equal(t, blockSequenceReorg[4].BlockIdentifier, head)
		assert.NoError(t, err)
	})
}
