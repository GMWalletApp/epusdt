package scanner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"strconv"
	"time"

	"github.com/tidwall/gjson"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

type tonCenter struct {
	*jsonSource
	protocol string
}

func newTonCenter(ctx context.Context, n Network, p string) (Source, error) {
	s := &tonCenter{&jsonSource{n: n, http: newNodeHTTPClient(25 * time.Second)}, p}
	if _, e := s.Head(ctx); e != nil {
		s.Close()
		return nil, e
	}
	return s, nil
}
func (s *tonCenter) method(ctx context.Context, name string, params map[string]any) (gjson.Result, error) {
	path := "/" + name
	var body any = params
	if s.protocol == "jsonrpc" {
		path = ""
		body = map[string]any{"id": 1, "jsonrpc": "2.0", "method": name, "params": params}
	}
	r, e := s.request(ctx, path, body)
	// Public TON Center plans can limit callers to one request per second.
	for attempt := 0; errors.Is(e, errRateLimited) && attempt < 3; attempt++ {
		select {
		case <-ctx.Done():
			return gjson.Result{}, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
		r, e = s.request(ctx, path, body)
	}
	if e != nil {
		return r, e
	}
	if r.Get("error").Exists() || r.Get("ok").Raw == "false" || !r.Get("result").IsObject() {
		return gjson.Result{}, errors.New("TON Center method failed: " + name)
	}
	return r.Get("result"), nil
}
func (s *tonCenter) Head(ctx context.Context) (int64, error) {
	r, e := s.method(ctx, "getMasterchainInfo", map[string]any{})
	if e != nil {
		return 0, e
	}
	if r.Get("init.root_hash").String() != "F6OpKZKqvqeFp6CQmFomXNMfMj2EnaUSOXN+Mh+wVWk=" {
		return 0, errors.New("unexpected TON genesis")
	}
	id, e := tonID(r.Get("last"))
	if e != nil {
		return 0, e
	}
	if id.Workchain != -1 {
		return 0, errors.New("expected TON masterchain")
	}
	return int64(id.SeqNo), nil
}
func tonID(r gjson.Result) (*ton.BlockIDExt, error) {
	wc, e1 := strconv.ParseInt(r.Get("workchain").String(), 10, 32)
	sh, e2 := strconv.ParseInt(r.Get("shard").String(), 10, 64)
	seq, e3 := strconv.ParseUint(r.Get("seqno").String(), 10, 32)
	root, e4 := base64.StdEncoding.DecodeString(r.Get("root_hash").String())
	file, e5 := base64.StdEncoding.DecodeString(r.Get("file_hash").String())
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || len(root) != 32 || len(file) != 32 {
		return nil, errors.New("invalid TON block ID")
	}
	return &ton.BlockIDExt{Workchain: int32(wc), Shard: sh, SeqNo: uint32(seq), RootHash: root, FileHash: file}, nil
}
func (s *tonCenter) LookupBlock(ctx context.Context, w int32, sh int64, seq uint32) (*ton.BlockIDExt, error) {
	r, e := s.method(ctx, "lookupBlock", map[string]any{"workchain": w, "shard": strconv.FormatInt(sh, 10), "seqno": seq})
	if e != nil {
		return nil, e
	}
	id, e := tonID(r)
	if e != nil {
		return nil, e
	}
	if id.Workchain != w || id.Shard != sh || id.SeqNo != seq {
		return nil, errors.New("TON lookup mismatch")
	}
	return id, nil
}
func (s *tonCenter) GetBlockShardsInfo(ctx context.Context, id *ton.BlockIDExt) ([]*ton.BlockIDExt, error) {
	r, e := s.method(ctx, "getShards", map[string]any{"seqno": id.SeqNo})
	if e != nil {
		return nil, e
	}
	rows := r.Get("shards")
	if !rows.IsArray() || len(rows.Array()) == 0 {
		return nil, errors.New("missing TON shard state")
	}
	var ids []*ton.BlockIDExt
	for _, row := range rows.Array() {
		v, e := tonID(row)
		if e != nil {
			return nil, e
		}
		ids = append(ids, v)
	}
	return ids, nil
}
func (s *tonCenter) GetBlockData(ctx context.Context, id *ton.BlockIDExt) (*tlb.Block, error) {
	r, e := s.method(ctx, "getBlock", map[string]any{"workchain": id.Workchain, "shard": strconv.FormatInt(id.Shard, 10), "seqno": id.SeqNo, "root_hash": base64.StdEncoding.EncodeToString(id.RootHash), "file_hash": base64.StdEncoding.EncodeToString(id.FileHash)})
	if e != nil {
		return nil, e
	}
	got, e := tonID(r.Get("id"))
	if e != nil || !id.Equals(got) {
		return nil, errors.New("TON block ID mismatch")
	}
	raw, e := base64.StdEncoding.DecodeString(r.Get("data").String())
	if e != nil {
		return nil, e
	}
	hash := sha256.Sum256(raw)
	if !bytes.Equal(hash[:], id.FileHash) {
		return nil, errors.New("TON block file hash mismatch")
	}
	c, e := cell.FromBOC(raw)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(c.Hash(), id.RootHash) {
		return nil, errors.New("TON block root hash mismatch")
	}
	var block tlb.Block
	if e = tlb.LoadFromCell(&block, c.BeginParse()); e != nil {
		return nil, e
	}
	return &block, nil
}
func (s *tonCenter) Read(ctx context.Context, h, head int64) (int64, []Activity, error) {
	return readTON(ctx, s, h)
}

// TONReadSlice runs a historical getter with one slice argument and one slice
// result. Payment consumers use it to verify contracts against the same block
// that produced an activity. The caller interprets the return value.
func TONReadSlice(ctx context.Context, n Network, master *ton.BlockIDExt, contract *address.Address, method string, arg *cell.Cell) (*cell.Slice, error) {
	if master == nil || master.Workchain != -1 || contract == nil || contract.Type() != address.StdAddress || arg == nil {
		return nil, errors.New("invalid TON state query")
	}
	if n.Name != "ton" || (n.Protocol != "rest" && n.Protocol != "jsonrpc") {
		return nil, errors.New("expected TON Center interface")
	}
	if err := ValidateNetwork(n); err != nil {
		return nil, err
	}
	s := &tonCenter{&jsonSource{n: n, http: newNodeHTTPClient(25 * time.Second)}, n.Protocol}
	defer s.Close()
	id, err := s.LookupBlock(ctx, -1, master.Shard, master.SeqNo)
	if err != nil {
		return nil, err
	}
	if !id.Equals(master) {
		return nil, errors.New("TON master block differs from scanned evidence")
	}
	r, err := s.method(ctx, "runGetMethod", map[string]any{"address": contract.StringRaw(), "method": method, "seqno": master.SeqNo, "stack": [][]string{{"tvm.Slice", base64.StdEncoding.EncodeToString(arg.ToBOC())}}})
	if err != nil {
		return nil, err
	}
	exit, err := strconv.Atoi(r.Get("exit_code").String())
	if err != nil || exit < 0 || exit > 1 || len(r.Get("stack").Array()) != 1 {
		return nil, errors.New("TON getter failed")
	}
	state, err := tonID(r.Get("block_id"))
	if err != nil {
		return nil, err
	}
	if !state.Equals(master) {
		shards, e := s.GetBlockShardsInfo(ctx, master)
		if e != nil {
			return nil, e
		}
		found := false
		for _, shard := range shards {
			if state.Equals(shard) && shard.Workchain == contract.Workchain() && tlb.ShardID(shard.Shard).ContainsAddress(contract) {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("TON getter returned state outside scanned master block")
		}
	}
	entry := r.Get("stack.0")
	switch entry.Get("0").String() {
	case "slice", "cell":
	default:
		return nil, errors.New("unexpected TON stack type")
	}
	raw, err := base64.StdEncoding.DecodeString(entry.Get("1.bytes").String())
	if err != nil {
		return nil, err
	}
	c, err := cell.FromBOC(raw)
	if err != nil {
		return nil, err
	}
	return c.BeginParse(), nil
}
