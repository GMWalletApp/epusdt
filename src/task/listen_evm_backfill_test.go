package task

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

func TestProcessEvmBackfillLogsUsesQuerySnapshotAsRecipientSource(t *testing.T) {
	recipient := common.HexToAddress("0x1111111111111111111111111111111111111111")
	contract := common.HexToAddress("0x2222222222222222222222222222222222222222")
	txHash := common.HexToHash("0x1234")
	called := false

	err := processEvmBackfillLogsWith(
		context.Background(),
		nil,
		mdb.NetworkBase,
		[]types.Log{{
			Address:     contract,
			Topics:      []common.Hash{transferEventHash, common.Hash{}, common.BytesToHash(recipient.Bytes())},
			Data:        big.NewInt(100).Bytes(),
			BlockNumber: 42,
			TxHash:      txHash,
		}},
		func(common.Address) bool { return false },
		func(context.Context, *ethclient.Client, uint64) (*types.Header, error) {
			return &types.Header{Time: 123}, nil
		},
		func(network string, gotContract, gotRecipient common.Address, amount *big.Int, gotTxHash string, blockTsMs int64) error {
			called = true
			if network != mdb.NetworkBase || gotContract != contract || gotRecipient != recipient {
				t.Fatalf("转账参数不匹配: network=%s contract=%s recipient=%s", network, gotContract.Hex(), gotRecipient.Hex())
			}
			if amount.Cmp(big.NewInt(100)) != 0 || gotTxHash != txHash.Hex() || blockTsMs != 123000 {
				t.Fatalf("转账数据不匹配: amount=%s hash=%s timestamp=%d", amount.String(), gotTxHash, blockTsMs)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("processEvmBackfillLogsWith() error = %v", err)
	}
	if !called {
		t.Fatal("批次查询已命中的收款地址未进入处理流程")
	}
}

func TestProcessEvmBackfillLogsReturnsTransferError(t *testing.T) {
	wantErr := errors.New("数据库暂时不可用")
	err := processEvmBackfillLogsWith(
		context.Background(),
		nil,
		mdb.NetworkArbitrum,
		[]types.Log{{
			Address:     common.HexToAddress("0x3333333333333333333333333333333333333333"),
			Topics:      []common.Hash{transferEventHash, common.Hash{}, common.HexToHash("0x4444")},
			Data:        big.NewInt(1).Bytes(),
			BlockNumber: 99,
			TxHash:      common.HexToHash("0x5678"),
		}},
		func(common.Address) bool { return true },
		func(context.Context, *ethclient.Client, uint64) (*types.Header, error) {
			return &types.Header{Time: 456}, nil
		},
		func(string, common.Address, common.Address, *big.Int, string, int64) error {
			return wantErr
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("processEvmBackfillLogsWith() error = %v, want %v", err, wantErr)
	}
	if isEvmBackfillRPCError(err) {
		t.Fatalf("订单处理错误不应计入 RPC 节点故障: %v", err)
	}
}

func TestProcessEvmBackfillLogsMarksHeaderFailureAsRPCError(t *testing.T) {
	wantErr := errors.New("区块头查询失败")
	err := processEvmBackfillLogsWith(
		context.Background(),
		nil,
		mdb.NetworkBase,
		[]types.Log{{
			Topics:      []common.Hash{transferEventHash, common.Hash{}, common.HexToHash("0x5555")},
			BlockNumber: 100,
		}},
		func(common.Address) bool { return true },
		func(context.Context, *ethclient.Client, uint64) (*types.Header, error) {
			return nil, wantErr
		},
		func(string, common.Address, common.Address, *big.Int, string, int64) error {
			t.Fatal("区块头失败后不应处理转账")
			return nil
		},
	)
	if !errors.Is(err, wantErr) || !isEvmBackfillRPCError(err) {
		t.Fatalf("header error = %v, want wrapped RPC error %v", err, wantErr)
	}
}

func TestEvmBackfillInitialLookbackCoversArbitrumFastBlocks(t *testing.T) {
	if got := evmBackfillInitialLookback(mdb.NetworkArbitrum); got != 8192 {
		t.Fatalf("Arbitrum lookback = %d, want 8192", got)
	}
	if got := evmBackfillInitialLookback(mdb.NetworkBase); got != evmBackfillInitialLookbackBlocks {
		t.Fatalf("Base lookback = %d, want %d", got, evmBackfillInitialLookbackBlocks)
	}
}
