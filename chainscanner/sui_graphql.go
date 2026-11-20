package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	pb "github.com/block-vision/sui-go-sdk/pb/sui/rpc/v2"
	"github.com/tidwall/gjson"
	"google.golang.org/protobuf/encoding/protojson"
)

const suiGraphEvents = `nodes{sender{address} transactionModule{name package{address}} contents{bcs type{repr}}} pageInfo{hasNextPage endCursor}`
const suiGraphTx = `digest sender{address} gasInput{gasSponsor{address}} effects{checkpoint{sequenceNumber timestamp} effectsJson balanceChangesJson events(first:4){` + suiGraphEvents + `}}`

func graphPage(c gjson.Result, seen map[string]bool) (string, error) {
	if !c.Get("nodes").IsArray() {
		return "", errors.New("missing GraphQL nodes")
	}
	switch c.Get("pageInfo.hasNextPage").Raw {
	case "false":
		return "", nil
	case "true":
		cursor := c.Get("pageInfo.endCursor").String()
		if cursor != "" && !seen[cursor] && len(c.Get("nodes").Array()) > 0 {
			seen[cursor] = true
			return cursor, nil
		}
	}
	return "", errors.New("invalid GraphQL page cursor")
}
func (s *graphSource) suiCheckpoint(ctx context.Context, h int64) (int64, []Activity, error) {
	const q = `query($height:UInt53!,$after:String){checkpoint(sequenceNumber:$height){sequenceNumber digest transactions(first:8,after:$after){nodes{` + suiGraphTx + `} pageInfo{hasNextPage endCursor}}}}`
	var after any
	var out []Activity
	var hash string
	seenPages := map[string]bool{}
	seenTx := map[string]bool{}
	for {
		r, e := s.query(ctx, q, map[string]any{"height": h, "after": after})
		if e != nil {
			return 0, nil, e
		}
		cp := r.Get("checkpoint")
		if cp.Get("sequenceNumber").String() != strconv.FormatInt(h, 10) || cp.Get("digest").String() == "" || hash != "" && hash != cp.Get("digest").String() {
			return 0, nil, errors.New("incomplete or changed Sui checkpoint")
		}
		hash = cp.Get("digest").String()
		page := cp.Get("transactions")
		next, e := graphPage(page, seenPages)
		if e != nil {
			return 0, nil, e
		}
		for _, t := range page.Get("nodes").Array() {
			ev, e := s.suiGraphActivity(ctx, t)
			if e != nil {
				return 0, nil, e
			}
			if ev.Height != h || seenTx[ev.TxID] {
				return 0, nil, errors.New("Sui checkpoint membership mismatch")
			}
			seenTx[ev.TxID] = true
			out = append(out, ev)
		}
		if next == "" {
			return h, out, nil
		}
		after = next
	}
}
func (s *graphSource) suiGraphActivity(ctx context.Context, t gjson.Result) (Activity, error) {
	digest := t.Get("digest").String()
	ef := t.Get("effects")
	effects := ef.Get("effectsJson")
	balances := ef.Get("balanceChangesJson")
	h, e := strconv.ParseInt(ef.Get("checkpoint.sequenceNumber").String(), 10, 64)
	ts, te := time.Parse(time.RFC3339Nano, ef.Get("checkpoint.timestamp").String())
	if e != nil || te != nil || digest == "" || effects.Get("transactionDigest").String() != digest || !effects.Get("status.success").Exists() || !balances.IsArray() {
		return Activity{}, errors.New("incomplete Sui GraphQL effects")
	}
	tx := map[string]any{"sender": t.Get("sender.address").String(), "gasPayment": map[string]any{"owner": t.Get("gasInput.gasSponsor.address").String()}}
	page := ef.Get("events")
	seen := map[string]bool{}
	var events []any
	for {
		next, e := graphPage(page, seen)
		if e != nil {
			return Activity{}, e
		}
		for _, v := range page.Get("nodes").Array() {
			events = append(events, map[string]any{"sender": v.Get("sender.address").String(), "packageId": v.Get("transactionModule.package.address").String(), "module": v.Get("transactionModule.name").String(), "eventType": v.Get("contents.type.repr").String(), "contents": map[string]any{"value": v.Get("contents.bcs").String()}})
		}
		if next == "" {
			break
		}
		r, e := s.query(ctx, `query($id:String!,$after:String!){transaction(digest:$id){digest effects{events(first:32,after:$after){`+suiGraphEvents+`}}}}`, map[string]any{"id": digest, "after": next})
		if e != nil {
			return Activity{}, e
		}
		if r.Get("transaction.digest").String() != digest {
			return Activity{}, errors.New("Sui events transaction mismatch")
		}
		page = r.Get("transaction.effects.events")
	}
	raw := object(map[string]any{"digest": digest, "transaction": tx, "effects": json.RawMessage(effects.Raw), "balanceChanges": json.RawMessage(balances.Raw), "events": map[string]any{"events": events}})
	var executed pb.ExecutedTransaction
	if e = (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &executed); e != nil {
		return Activity{}, e
	}
	return suiActivity(&executed, h, ts.UnixMilli())
}
