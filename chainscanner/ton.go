package scanner

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"time"
)

type TONData struct {
	Account string
	BOC     []byte
	Master  *ton.BlockIDExt
}
type tonSource struct {
	pool *liteclient.ConnectionPool
	api  ton.APIClientWrapped
}

func newTON(ctx context.Context, n Network) (Source, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cfg, err := liteclient.GetConfigFromUrl(ctx, n.URL)
	if err != nil {
		return nil, err
	}
	pool := liteclient.NewConnectionPool()
	if err = pool.AddConnectionsFromConfig(ctx, cfg); err != nil {
		pool.Stop()
		return nil, err
	}
	client := ton.NewAPIClient(pool, ton.ProofCheckPolicyFast)
	client.SetTrustedBlockFromConfig(cfg)
	return &tonSource{pool, client.WithRetry(2).WithTimeout(15 * time.Second)}, nil
}
func (s *tonSource) Close() { s.pool.Stop() }
func (s *tonSource) Head(ctx context.Context) (int64, error) {
	b, e := s.api.CurrentMasterchainInfo(ctx)
	if e != nil {
		return 0, e
	}
	return int64(b.SeqNo), nil
}
func (s *tonSource) Read(ctx context.Context, h, head int64) (int64, []Activity, error) {
	return readTON(ctx, s.api, h)
}

type tonBlockReader interface {
	LookupBlock(context.Context, int32, int64, uint32) (*ton.BlockIDExt, error)
	GetBlockShardsInfo(context.Context, *ton.BlockIDExt) ([]*ton.BlockIDExt, error)
	GetBlockData(context.Context, *ton.BlockIDExt) (*tlb.Block, error)
}

func readTON(ctx context.Context, api tonBlockReader, h int64) (int64, []Activity, error) {
	if h == 0 {
		return 0, nil, nil
	}
	master, err := api.LookupBlock(ctx, -1, -1<<63, uint32(h))
	if err != nil {
		return 0, nil, err
	}
	prev, err := api.LookupBlock(ctx, -1, -1<<63, uint32(h-1))
	if err != nil {
		return 0, nil, err
	}
	old, err := api.GetBlockShardsInfo(ctx, prev)
	if err != nil {
		return 0, nil, err
	}
	shards, err := api.GetBlockShardsInfo(ctx, master)
	if err != nil {
		return 0, nil, err
	}
	visited := map[string]bool{}
	for _, b := range old {
		visited[string(b.RootHash)] = true
	}
	var events []Activity
	var collect func(*ton.BlockIDExt, int) error
	collect = func(id *ton.BlockIDExt, depth int) error {
		key := string(id.RootHash)
		if visited[key] || id.SeqNo == 0 {
			return nil
		}
		if depth > 256 {
			return errors.New("TON shard ancestry exceeds one master interval")
		}
		block, err := api.GetBlockData(ctx, id)
		if err != nil {
			return err
		}
		if id.Workchain != -1 {
			parents, err := ton.GetParentBlocks(&block.BlockInfo)
			if err != nil {
				return err
			}
			for _, p := range parents {
				if err = collect(p, depth+1); err != nil {
					return err
				}
			}
		}
		rows, err := tonTransactions(block, id.Workchain, master)
		if err != nil {
			return err
		}
		events = append(events, rows...)
		visited[key] = true
		return nil
	}
	for _, b := range append(shards, master) {
		if err = collect(b, 0); err != nil {
			return 0, nil, err
		}
	}
	return h, events, nil
}
func tonTransactions(block *tlb.Block, wc int32, master *ton.BlockIDExt) ([]Activity, error) {
	if block.Extra == nil || block.Extra.ShardAccountBlocks == nil {
		return nil, errors.New("missing TON account blocks")
	}
	var accounts tlb.ShardAccountBlocks
	if err := tlb.LoadFromCell(&accounts, block.Extra.ShardAccountBlocks.BeginParse()); err != nil {
		return nil, err
	}
	rows, err := accounts.Accounts.LoadAll()
	if err != nil {
		return nil, err
	}
	var out []Activity
	for _, row := range rows {
		if err = tlb.LoadFromCell(new(tlb.CurrencyCollection), row.Value); err != nil {
			return nil, err
		}
		var acc tlb.AccountBlock
		if err = tlb.LoadFromCell(&acc, row.Value); err != nil {
			return nil, err
		}
		txs, err := acc.Transactions.LoadAll()
		if err != nil {
			return nil, err
		}
		for _, v := range txs {
			if err = tlb.LoadFromCell(new(tlb.CurrencyCollection), v.Value); err != nil {
				return nil, err
			}
			c, err := v.Value.LoadRefCell()
			if err != nil {
				return nil, err
			}
			var tx tlb.Transaction
			if err = tlb.LoadFromCell(&tx, c.BeginParse()); err != nil {
				return nil, err
			}
			account := address.NewAddress(0, byte(wc), tx.AccountAddr).StringRaw()
			e := Activity{Height: int64(master.SeqNo), TxID: hex.EncodeToString(c.Hash()), TimeMS: int64(tx.Now) * 1000, Addresses: []string{account}, Data: object(TONData{account, c.ToBOC(), master})}
			index := func(m tlb.Message) {
				for _, a := range []*address.Address{m.Msg.SenderAddr(), m.Msg.DestAddr()} {
					if a != nil && a.Type() == address.StdAddress {
						e.Addresses = append(e.Addresses, a.StringRaw())
					}
				}
			}
			if tx.IO.In != nil {
				index(*tx.IO.In)
			}
			if tx.IO.Out != nil {
				msgs, err := tx.IO.Out.ToSlice()
				if err != nil {
					return nil, err
				}
				for _, m := range msgs {
					index(m)
				}
			}
			out = append(out, e)
		}
	}
	return out, nil
}
