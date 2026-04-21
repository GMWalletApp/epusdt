package walletaddr

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/assimon/luuu/model/mdb"
	"github.com/btcsuite/btcutil/base58"
	"github.com/gagliardetto/solana-go"
)

func NormalizeNetwork(network string) string {
	return strings.ToLower(strings.TrimSpace(network))
}

func IsEVMNetwork(network string) bool {
	switch NormalizeNetwork(network) {
	case mdb.NetworkEthereum, mdb.NetworkBsc, mdb.NetworkPolygon, mdb.NetworkPlasma:
		return true
	}
	return false
}

func Normalize(network, address string) string {
	address = strings.TrimSpace(address)
	if IsEVMNetwork(network) {
		return strings.ToLower(address)
	}
	return address
}

func Validate(network, address string) bool {
	address = strings.TrimSpace(address)
	switch NormalizeNetwork(network) {
	case mdb.NetworkTron:
		return isValidTronAddress(address)
	case mdb.NetworkSolana:
		return isValidSolanaAddress(address)
	case mdb.NetworkEthereum, mdb.NetworkBsc, mdb.NetworkPolygon, mdb.NetworkPlasma:
		return isValidEVMAddress(address)
	default:
		return false
	}
}

func isValidEVMAddress(address string) bool {
	if len(address) != 42 || !strings.HasPrefix(address, "0x") {
		return false
	}
	_, err := hex.DecodeString(address[2:])
	return err == nil
}

func isValidTronAddress(address string) bool {
	if len(address) < 26 || len(address) > 35 || address[0] != 'T' {
		return false
	}
	decoded := base58.Decode(address)
	if len(decoded) != 25 {
		return false
	}
	if decoded[0] != 0x41 {
		return false
	}
	payload := decoded[:21]
	checksum := decoded[21:]
	hash := sha256.Sum256(payload)
	hash2 := sha256.Sum256(hash[:])
	return string(checksum) == string(hash2[:4])
}

func isValidSolanaAddress(address string) bool {
	if len(address) < 32 || len(address) > 44 {
		return false
	}
	_, err := solana.PublicKeyFromBase58(address)
	return err == nil
}
