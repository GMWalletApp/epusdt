package chains

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
)

// The index contributes a location, never a vote or any unchecked output fields.
type TONLocation struct {
	account     *address.Address
	lt          uint64
	master, seq uint32
	shard       int64
}

func LocateTON(ctx context.Context, n Network, hash string) (TONLocation, error) {
	s := &jsonSource{n: n, http: newNodeHTTPClient(6 * time.Second)}
	defer s.Close()
	row, err := s.tonIndexTransaction(ctx, hash)
	if err != nil {
		return TONLocation{}, err
	}
	var h struct {
		Account, Lt string
		Master      uint32 `json:"mc_block_seqno"`
		Block       struct {
			Workchain *int32
			Shard     string
			Seqno     uint32
		} `json:"block_ref"`
	}
	if err = json.Unmarshal(row, &h); err != nil {
		return TONLocation{}, err
	}
	a, err := address.ParseRawAddr(h.Account)
	if err != nil || len(a.Data()) != 32 {
		return TONLocation{}, ErrTransactionData
	}
	lt, err := strconv.ParseUint(h.Lt, 10, 64)
	if err != nil {
		return TONLocation{}, err
	}
	sh, err := strconv.ParseUint(h.Block.Shard, 16, 64)
	if err != nil || lt == 0 || sh == 0 || h.Master == 0 || h.Block.Seqno == 0 || h.Block.Workchain == nil || *h.Block.Workchain != a.Workchain() || !tlb.ShardID(sh).ContainsAddress(a) {
		return TONLocation{}, ErrTransactionData
	}
	return TONLocation{a, lt, h.Master, h.Block.Seqno, int64(sh)}, nil
}

// Only pinned direct peers can vote. A global-config URL may select the same
// backing peer twice. Both socket host and Ed25519 identity are deduplicated.
func TONVoter(n Network) (host, identity string, ok bool) {
	if n.Name != "ton" || n.Protocol != "lite" {
		return
	}
	u, err := url.Parse(n.URL)
	if err != nil || u.Scheme != "adnl" {
		return
	}
	key, err := tonPeerKey(u)
	if err != nil {
		return
	}
	return u.Hostname(), base64.RawURLEncoding.EncodeToString(key), true
}

func VerifyTON(ctx context.Context, n Network, hash string, loc TONLocation) (json.RawMessage, error) {
	if _, _, ok := TONVoter(n); !ok {
		return nil, ErrTransactionData
	}
	src, err := newTON(ctx, n)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return verifyLocatedTON(ctx, src.(*tonSource).api, hash, loc)
}

func verifyLocatedTON(ctx context.Context, api ton.APIClientWrapped, hash string, loc TONLocation) (json.RawMessage, error) {
	master, err := api.LookupBlock(ctx, -1, -1<<63, loc.master)
	if err != nil {
		return nil, err
	}
	if master.SeqNo != loc.master || master.Workchain != -1 || master.Shard != -1<<63 || len(master.RootHash) != 32 {
		return nil, ErrTransactionData
	}
	block := master
	if loc.account.Workchain() != -1 {
		block, err = locatedShard(ctx, api, master, loc)
		if err != nil {
			return nil, err
		}
	}
	if block.Workchain != loc.account.Workchain() || block.Shard != loc.shard || block.SeqNo != loc.seq {
		return nil, ErrTransactionData
	}
	// GetTransaction checks the Merkle proof against this block's root.
	tx, err := api.GetTransaction(ctx, block, loc.account, loc.lt)
	if err != nil {
		return nil, err
	}
	if hex.EncodeToString(tx.Hash) != hash || tx.LT != loc.lt || !bytes.Equal(tx.AccountAddr, loc.account.Data()) {
		return nil, ErrTransactionData
	}
	c, err := tlb.ToCell(tx)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(c.Hash(), tx.Hash) {
		return nil, ErrTransactionData
	}
	return json.Marshal(map[string]any{"account": loc.account.StringRaw(), "lt": strconv.FormatUint(tx.LT, 10), "hash": tx.Hash, "now": tx.Now, "mc_block_seqno": master.SeqNo, "mc_block_root_hash": master.RootHash, "block_root_hash": block.RootHash, "block_ref": map[string]any{"workchain": block.Workchain, "shard": strconv.FormatUint(uint64(block.Shard), 16), "seqno": block.SeqNo}, "transaction_boc": c.ToBOC()})
}

func accountShard(blocks []*ton.BlockIDExt, a *address.Address) (*ton.BlockIDExt, error) {
	for _, b := range blocks {
		if b != nil && b.Workchain == a.Workchain() && tlb.ShardID(b.Shard).ContainsAddress(a) {
			return b, nil
		}
	}
	return nil, errors.New("TON account shard not found")
}

// Follow the account's shard through splits/merges between two authenticated
// masterchain shard snapshots. Reject an index pointing at a later master.
func locatedShard(ctx context.Context, api ton.APIClientWrapped, master *ton.BlockIDExt, loc TONLocation) (*ton.BlockIDExt, error) {
	prev, err := api.LookupBlock(ctx, -1, -1<<63, master.SeqNo-1)
	if err != nil {
		return nil, err
	}
	if prev.Workchain != -1 || prev.Shard != master.Shard || prev.SeqNo+1 != master.SeqNo {
		return nil, ErrTransactionData
	}
	old, err := api.GetBlockShardsInfo(ctx, prev)
	if err != nil {
		return nil, err
	}
	before, err := accountShard(old, loc.account)
	if err != nil {
		return nil, err
	}
	current, err := api.GetBlockShardsInfo(ctx, master)
	if err != nil {
		return nil, err
	}
	b, err := accountShard(current, loc.account)
	if err != nil {
		return nil, err
	}
	for depth := 0; depth < 256 && b.SeqNo >= loc.seq && !b.Equals(before); depth++ {
		if b.SeqNo == loc.seq && b.Shard == loc.shard {
			return b, nil
		}
		header, err := api.GetBlockData(ctx, b)
		if err != nil {
			return nil, err
		}
		parents, err := ton.GetParentBlocks(&header.BlockInfo)
		if err != nil {
			return nil, err
		}
		b, err = accountShard(parents, loc.account)
		if err != nil {
			return nil, err
		}
	}
	return nil, ErrTransactionData
}
