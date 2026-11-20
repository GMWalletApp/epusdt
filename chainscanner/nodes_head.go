package scanner

import (
	"context"

	geyser "github.com/rpcpool/yellowstone-grpc/examples/golang/proto"
)

// A paired scanner can follow a history provider's cursor. A node test must
// report a height observed on the selected primary interface.
func nodeHead(ctx context.Context, src Source, n Network) (int64, error) {
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
