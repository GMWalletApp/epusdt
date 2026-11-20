package chains

import (
	"context"
	"errors"
	"strconv"
	"time"

	geyser "github.com/rpcpool/yellowstone-grpc/examples/golang/proto"
)

// A paired scanner can follow a history provider's cursor. A node test must
// report a height observed on the selected primary interface.
func NodeHead(ctx context.Context, src Source, n Network) (int64, error) {
	pair, ok := src.(*pairedSource)
	if !ok {
		return src.Head(ctx)
	}
	var height int64
	accept := func(h int64) bool { height = h; return true }
	switch {
	case n.Protocol == "sse":
		err := beaconHeads(ctx, n, accept)
		return height, err
	case n.Name == "solana" && n.Protocol == "ws":
		err := solanaRoots(ctx, n, accept)
		return height, err
	case n.Name == "solana" && n.Protocol == "grpc":
		level := geyser.CommitmentLevel_FINALIZED
		r, err := geyser.NewGeyserClient(pair.conn).GetSlot(grpcAuth(ctx, "x-token", n.APIKey), &geyser.GetSlotRequest{Commitment: &level})
		if err != nil {
			return 0, err
		}
		return int64(r.GetSlot()), nil
	case n.Name == "aptos" && n.Protocol == "grpc":
		return pair.verifiedVersion, nil
	case pair.indexer != nil:
		return pair.indexer.aptosVersion(ctx)
	default:
		return src.Head(ctx)
	}
}
func UniSatHeight(ctx context.Context, n Network) (int64, error) {
	s := &jsonSource{n: n, http: newNodeHTTPClient(10 * time.Second)}
	defer s.Close()
	v, err := s.request(ctx, "/v1/indexer/blockchain/info", nil)
	if err != nil {
		return 0, err
	}
	if v.Get("code").Raw != "0" || v.Get("data.chain").String() != "main" {
		return 0, errors.New("invalid Bitcoin mainnet response")
	}
	return strconv.ParseInt(v.Get("data.blocks").Raw, 10, 64)
}

// Bitcoin Core nodes expose a mainnet height for the node inventory.
func BitcoinRPCHeight(ctx context.Context, n Network) (int64, error) {
	s := &jsonSource{n: n, http: newNodeHTTPClient(10 * time.Second)}
	defer s.Close()
	r, err := s.request(ctx, "", map[string]any{"jsonrpc": "2.0", "method": "getblockchaininfo", "params": []any{}, "id": 1})
	if err != nil {
		return 0, err
	}
	if e := r.Get("error"); (e.Exists() && e.Raw != "null") || r.Get("result.chain").String() != "main" {
		return 0, errors.New("invalid Bitcoin mainnet response")
	}
	h, err := strconv.ParseInt(r.Get("result.blocks").Raw, 10, 64)
	if err != nil || h < 0 {
		return 0, errors.New("invalid Bitcoin block height")
	}
	return h, nil
}
