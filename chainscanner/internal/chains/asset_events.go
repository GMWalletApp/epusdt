package chains

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tidwall/gjson"
)

// AssetEventRules maps an emitting contract and topic0 to its fixed ABI layout.
// Treat a configured rule set as immutable after handing it to a scanner.
type AssetEventRules map[common.Address]map[common.Hash]AssetEventLayout

type AssetEventLayout struct {
	Topics       int   `json:"topics"`                  // includes topic0
	DataWords    int   `json:"data_words"`              // exact number of non-indexed, static 32-byte words
	AddressWords []int `json:"address_words,omitempty"` // zero-based offsets of address arguments
	RefreshAsset bool  `json:"refresh_asset,omitempty"` // emitting token has a global balance update
}

var ethereumAssetEvents = AssetEventRules{
	// Aave V3 Ethereum Core Pool: supply on behalf of another account.
	common.HexToAddress("0x87870Bca3F3fD6335C3F4ce8392D69350B4fA4E2"): {
		crypto.Keccak256Hash([]byte("Supply(address,address,address,uint256,uint16)")): {Topics: 4, DataWords: 2, AddressWords: []int{0}},
	},
	// Lido stETH proxy: TokenRebased identifies the token, not individual holders.
	common.HexToAddress("0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84"): {
		crypto.Keccak256Hash([]byte("TokenRebased(uint256,uint256,uint256,uint256,uint256,uint256,uint256)")): {Topics: 2, DataWords: 6, RefreshAsset: true},
	},
}

func (n Network) ValidateAssetEvents() error {
	if n.AssetEvents == nil {
		return nil
	}
	if !IsEVM(n.Name) {
		return fmt.Errorf("asset_events requires an EVM network, got %q", n.Name)
	}
	for contract, signatures := range *n.AssetEvents {
		for sig, layout := range signatures {
			if layout.Topics < 1 || layout.Topics > 4 || layout.DataWords < 0 || (len(layout.AddressWords) == 0 && !layout.RefreshAsset) {
				return fmt.Errorf("invalid asset_events layout for %s %s", contract, sig)
			}
			for i, word := range layout.AddressWords {
				if word < 0 || word >= layout.DataWords || slices.Contains(layout.AddressWords[:i], word) {
					return fmt.Errorf("invalid asset_events address word %d for %s %s", word, contract, sig)
				}
			}
		}
	}
	return nil
}

// indexEVMLog serves both the JSON-RPC and GraphQL block readers. All extra
// matches come from retained log evidence and require no further node reads.
func (n Network) indexEVMLog(addresses *[]string, evidence *Evidence, emitter string, topics []gjson.Result, log gjson.Result) {
	addAddress(addresses, n.Name, emitter)
	if len(topics) == 0 {
		return
	}
	indexTopics(addresses, n.Name, topics[1:])
	if n.AssetEvents == nil && n.Name != "ethereum" {
		return
	}
	if !common.IsHexAddress(emitter) {
		return
	}
	contract := common.HexToAddress(emitter)
	var custom, defaults map[common.Hash]AssetEventLayout
	if n.AssetEvents != nil {
		custom = (*n.AssetEvents)[contract]
	}
	if n.Name == "ethereum" {
		defaults = ethereumAssetEvents[contract]
	}
	if len(custom)+len(defaults) == 0 {
		return
	}
	var signature common.Hash
	if signature.UnmarshalText([]byte(topics[0].String())) != nil {
		return
	}
	layout, ok := custom[signature]
	if !ok {
		layout, ok = defaults[signature]
	}
	if !ok || len(topics) != layout.Topics || log.Get("removed").Bool() {
		return
	}
	for _, topic := range topics[1:] {
		var word common.Hash
		if word.UnmarshalText([]byte(topic.String())) != nil {
			return
		}
	}
	raw := log.Get("data").String()
	if len(raw) < 2 || (len(raw)-2)%64 != 0 || (len(raw)-2)/64 != layout.DataWords {
		return
	}
	payload, err := hexutil.Decode(raw)
	if err != nil {
		return
	}
	// Validate every selected word before adding any participants.
	var padding [12]byte
	for _, i := range layout.AddressWords {
		if !bytes.Equal(payload[32*i:32*i+12], padding[:]) {
			return
		}
	}
	for _, i := range layout.AddressWords {
		*addresses = append(*addresses, hexutil.Encode(payload[32*i+12:32*i+32]))
	}
	if layout.RefreshAsset {
		token := strings.ToLower(contract.Hex())
		if !slices.Contains(evidence.RefreshAssets, token) {
			evidence.RefreshAssets = append(evidence.RefreshAssets, token)
		}
	}
}
