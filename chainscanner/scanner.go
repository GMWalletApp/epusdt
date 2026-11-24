// Package scanner follows blockchain activity and delivers it to address subscribers.
package scanner

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
	"github.com/GMWalletApp/ChainScanner/internal/nodes"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

type (
	AssetEventRules  = chains.AssetEventRules
	AssetEventLayout = chains.AssetEventLayout
	Network          = chains.Network
	Source           = chains.Source
	Activity         = chains.Activity
	Evidence         = chains.Evidence
	TONData          = chains.TONData

	Store               = engine.Store
	Watch               = feed.Watch
	Registration        = feed.Registration
	Termination         = feed.Termination
	SubscriptionReceipt = feed.SubscriptionReceipt
	Delivery            = feed.Delivery
	Feed                = feed.Feed
	Client              = feed.Client

	SourcePool             = nodes.SourcePool
	MigrationManifest      = nodes.MigrationManifest
	ManagementCapabilities = nodes.ManagementCapabilities
	RouteInventory         = nodes.RouteInventory
	NodeRoute              = nodes.Route
	NodeRegistry           = nodes.NodeRegistry
	Node                   = nodes.Node
	NodeOptions            = nodes.NodeOptions
	NodeEndpoint           = nodes.NodeEndpoint
)

var (
	ErrNetworkNotConfigured = engine.ErrNetworkNotConfigured
	ErrCursorNotReady       = feed.ErrCursorNotReady
)

// Open opens the scanner's persistent SQLite store.
func Open(path string) (*Store, error) { return engine.Open(path) }

// Handler serves the consumer API using a separate bearer token per consumer.
func Handler(s *Store, consumers map[string]string) (http.Handler, error) {
	return engine.ConsumerHandler(s, consumers)
}

// NewNodeRegistry loads built-in, configured and persisted nodes.
func NewNodeRegistry(ctx context.Context, s *Store, networks []Network) (*NodeRegistry, error) {
	return nodes.NewNodeRegistry(ctx, s, networks)
}

// NewNodeRegistryWithBlockedHosts also excludes matching endpoint hosts and their subdomains.
func NewNodeRegistryWithBlockedHosts(ctx context.Context, s *Store, networks []Network, hosts []string) (*NodeRegistry, error) {
	return nodes.NewNodeRegistry(ctx, s, networks, hosts...)
}

// NewSource opens the configured node using the network’s selected protocol.
func NewSource(ctx context.Context, n Network) (Source, error) { return chains.NewSource(ctx, n) }

// NewSourcePool distributes ranges across nodes of a single concrete network.
func NewSourcePool(ctx context.Context, network string) *SourcePool {
	return nodes.NewSourcePool(ctx, network)
}

// Wakeups returns a source’s optional channel of new head notifications.
func Wakeups(s Source) <-chan struct{} { return chains.Wakeups(s) }

// IsEVM reports whether the network uses the EVM adapter.
func IsEVM(name string) bool { return chains.IsEVM(name) }

// EVMChainID returns the configured EVM chain ID, or zero for other networks.
func EVMChainID(name string) int64 { return chains.EVMChainID(name) }

// Supported reports whether the scanner can read transactions on the network.
func Supported(name string) bool { return chains.Supported(name) }

// NativeAsset returns the native top-level transfer asset when supported.
func NativeAsset(name string) string { return chains.NativeAsset(name) }

// Canonical normalizes an address for comparison on the given network.
func Canonical(n, a string) (string, error) { return chains.Canonical(n, a) }

// Protocols lists the node protocols accepted for a network, in preference order.
func Protocols(network string) []string { return chains.Protocols(network) }

// NeedsHistory reports whether a transport requires a companion history endpoint.
func NeedsHistory(network, protocol string) bool { return chains.NeedsHistory(network, protocol) }

// ValidateEndpoint checks a primary endpoint before selecting a history node.
func ValidateEndpoint(n Network) error { return chains.ValidateEndpoint(n) }

// ValidateNetwork validates a complete network configuration.
func ValidateNetwork(n Network) error { return chains.ValidateNetwork(n) }

// SuiTransaction reads a Sui transaction by digest.
func SuiTransaction(ctx context.Context, n Network, digest string) (Activity, error) {
	return chains.SuiTransaction(ctx, n, digest)
}

// TONReadSlice runs a TON contract getter and returns its result as a slice.
func TONReadSlice(ctx context.Context, n Network, master *ton.BlockIDExt, contract *address.Address, method string, arg *cell.Cell) (*cell.Slice, error) {
	return chains.TONReadSlice(ctx, n, master, contract, method, arg)
}

// ConfigureNetworks supplies an embedded store with raw chain heads for subscription boundaries.
// Call before serving requests. The standalone node registry configures this automatically.
func ConfigureNetworks(store *Store, networks map[string]Network, head func(context.Context, string) (int64, error)) {
	engine.ConfigureNetworks(store, networks, head)
}

// ManagementProtocols lists admin-selectable transports, including height and strict lookup endpoints.
func ManagementProtocols() map[string][]string { return nodes.ManagementProtocols() }

// ConnectTONPeer opens a mainnet-verified ADNL connection for a selected registry endpoint.
func ConnectTONPeer(ctx context.Context, pool *liteclient.ConnectionPool, raw string) error {
	return chains.ConnectTONPeer(ctx, pool, raw)
}

// PaymentEvidence normalizes chain-native hash lookup data for a payment consumer.
func PaymentEvidence(network string, raw json.RawMessage) (json.RawMessage, error) {
	n := chains.Network{Name: network}
	if network == "ton" {
		n.Protocol = "toncenter-v3"
		var proof struct {
			BOC string `json:"transaction_boc"`
		}
		if err := json.Unmarshal(raw, &proof); err != nil {
			return nil, err
		}
		if proof.BOC != "" {
			n.Protocol = "lite"
		}
	}
	return chains.TransactionEvidence(n, raw)
}
