package chains

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tidwall/gjson"
)

func TestAssetEventMatching(t *testing.T) {
	const pool = "0x87870bca3f3fd6335c3f4ce8392d69350b4fa4e2"
	const stETH = "0xae7ab96520de3a18e5e111b5eaab095312d7fe84"
	const other = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	supply := crypto.Keccak256Hash([]byte("Supply(address,address,address,uint256,uint16)")).Hex()
	rebase := crypto.Keccak256Hash([]byte("TokenRebased(uint256,uint256,uint256,uint256,uint256,uint256,uint256)")).Hex()
	indexed := fmt.Sprintf("0x%064x", 1)
	supplyTopics := []string{supply, indexed, indexed, indexed}
	// The numeric amount looks like another wallet. Only argument zero is an address.
	words := strings.Repeat("0", 24) + wallet[2:] + strings.Repeat("0", 24) + other[2:]
	for _, tc := range []struct {
		name, network, emitter, data string
		topics                       []string
		match, refresh               bool
	}{
		{"supply", "ethereum", pool, "0x" + words, supplyTopics, true, false},
		{"other deployment", "base", pool, "0x" + words, supplyTopics, false, false},
		{"other emitter", "ethereum", stETH, "0x" + words, supplyTopics, false, false},
		{"other signature", "ethereum", pool, "0x" + words, []string{indexed, indexed, indexed, indexed}, false, false},
		{"bad topics", "ethereum", pool, "0x" + words, supplyTopics[:3], false, false},
		{"bad hex", "ethereum", pool, "0x" + words[:127] + "z", supplyTopics, false, false},
		{"short data", "ethereum", pool, "0x" + words[:64], supplyTopics, false, false},
		{"bad address padding", "ethereum", pool, "0xf" + words[1:], supplyTopics, false, false},
		{"token rebase", "ethereum", stETH, "0x" + strings.Repeat("0", 384), []string{rebase, indexed}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := gjson.ParseBytes(object(map[string]any{"topics": tc.topics, "data": tc.data}))
			var addresses []string
			var ev Evidence
			for range 2 {
				Network{Name: tc.network}.indexEVMLog(&addresses, &ev, tc.emitter, log.Get("topics").Array(), log)
			}
			if slices.Contains(addresses, wallet) != tc.match || slices.Contains(addresses, other) {
				t.Fatalf("wrong participant matches: %v", addresses)
			}
			if tc.refresh && !slices.Equal(ev.RefreshAssets, []string{stETH}) || !tc.refresh && len(ev.RefreshAssets) != 0 {
				t.Fatalf("wrong refresh signals: %v", ev.RefreshAssets)
			}
		})
	}
}

func TestCustomAssetEventRules(t *testing.T) {
	const emitter = "0x2222222222222222222222222222222222222222"
	sig := crypto.Keccak256Hash([]byte("RewardPaid(address,uint256)")).Hex()
	var n Network
	config := fmt.Sprintf(`{"name":"base","asset_events":{"%s":{"%s":{"topics":1,"data_words":2,"address_words":[0]}}}}`, emitter, sig)
	if err := json.Unmarshal([]byte(config), &n); err != nil {
		t.Fatal(err)
	}
	if err := n.ValidateAssetEvents(); err != nil {
		t.Fatal(err)
	}
	log := gjson.ParseBytes(object(map[string]any{"topics": []string{sig}, "data": "0x" + strings.Repeat("0", 24) + wallet[2:] + strings.Repeat("0", 64)}))
	var addresses []string
	n.indexEVMLog(&addresses, &Evidence{}, emitter, log.Get("topics").Array(), log)
	if !slices.Contains(addresses, wallet) {
		t.Fatalf("reward beneficiary missing: %v", addresses)
	}
	for _, invalid := range []string{strings.Replace(config, "base", "solana", 1), strings.Replace(config, "[0]", "[2]", 1), strings.Replace(config, "[0]", "[0,0]", 1), strings.Replace(config, sig, "0x01", 1)} {
		var bad Network
		if err := json.Unmarshal([]byte(invalid), &bad); err == nil && bad.ValidateAssetEvents() == nil {
			t.Fatal("accepted invalid rule", invalid)
		}
	}
}
