package diagnostics

import "context"

type orderKey struct{}

// Attach stable order identifiers to scanner requests and their node attempts.
func WithOrder(ctx context.Context, trade, parent, merchant string, id uint64) context.Context {
	return context.WithValue(ctx, orderKey{}, Event{Trade: trade, Parent: parent, MerchantOrder: merchant, Order: id})
}
func Order(ctx context.Context) Event {
	e, _ := ctx.Value(orderKey{}).(Event)
	return e
}
