package service

import (
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/tidwall/gjson"
)

func AptosGetLedgerVersion() (int64, error) {
	body, err := aptosGet("/v1")
	if err != nil {
		return 0, err
	}
	latest := gjson.GetBytes(body, "ledger_version").Int()
	log.Sugar.Debugf("[APTOS] latest ledger version=%d", latest)
	return latest, nil
}

func AptosGetTransactionByHash(txID string) ([]byte, error) {
	txID = normalizeAptosTxID(txID)
	log.Sugar.Infof("[APTOS] fetch transaction by hash tx=%s", txID)
	return aptosGet(fmt.Sprintf("/v1/transactions/by_hash/%s", txID))
}

func AptosGetTransactions(start int64, limit int64) ([]byte, error) {
	if start < 0 {
		start = 0
	}
	if limit <= 0 {
		limit = 1
	}
	log.Sugar.Debugf("[APTOS] fetch transactions start=%d limit=%d", start, limit)
	return aptosGet(fmt.Sprintf("/v1/transactions?start=%d&limit=%d", start, limit))
}

func ParseAptosTransfers(body []byte, address string, tokens []mdb.ChainToken) ([]MoveObservedTransfer, error) {
	receive, err := addressutil.NormalizeMoveAddress(address)
	if err != nil {
		return nil, err
	}
	return ParseAptosTransfersForWallets(body, map[string]struct{}{receive: {}}, tokens)
}

func ParseAptosTransfersForWallets(body []byte, wallets map[string]struct{}, tokens []mdb.ChainToken) ([]MoveObservedTransfer, error) {
	root := gjson.ParseBytes(body)
	if !root.IsArray() {
		return nil, fmt.Errorf("unexpected Aptos transactions response")
	}
	var out []MoveObservedTransfer
	for _, tx := range root.Array() {
		if tx.Get("type").String() != "" && tx.Get("type").String() != "user_transaction" {
			continue
		}
		if !tx.Get("success").Bool() {
			continue
		}
		txID := strings.TrimSpace(tx.Get("hash").String())
		if txID == "" {
			continue
		}
		blockTimeMs := parseAptosTimestampMs(tx.Get("timestamp"))
		version := tx.Get("version").Int()
		txctx := buildAptosTransferContext(tx)
		events := buildAptosFungibleEvents(tx, txctx, tokens)
		deposits := make([]aptosFungibleEvent, 0)
		withdrawals := make(map[string]int)
		for _, event := range events {
			if event.token == nil {
				continue
			}
			switch event.action {
			case "withdraw":
				withdrawals[aptosTransferMatchKey(event.token.Symbol, event.rawAmount)]++
			case "deposit":
				deposits = append(deposits, event)
			}
		}
		usedDeposits := make([]bool, len(deposits))
		for i, event := range deposits {
			matchKey := aptosTransferMatchKey(event.token.Symbol, event.rawAmount)
			if withdrawals[matchKey] <= 0 {
				continue
			}
			if appendAptosObservedTransfer(&out, txID, version, blockTimeMs, event, wallets) {
				withdrawals[matchKey]--
				usedDeposits[i] = true
			} else {
				logAptosUnwatchedDeposit(txID, version, event, "direct")
			}
		}
		for i := 0; i < len(deposits); i++ {
			if usedDeposits[i] {
				continue
			}
			for j := i + 1; j < len(deposits); j++ {
				if usedDeposits[j] || !aptosDepositPairCanMatch(deposits[i], deposits[j]) {
					continue
				}
				sum := new(big.Int).Add(deposits[i].rawAmount, deposits[j].rawAmount)
				matchKey := aptosTransferMatchKey(deposits[i].token.Symbol, sum)
				if withdrawals[matchKey] <= 0 {
					continue
				}
				appendedFirst := appendAptosObservedTransfer(&out, txID, version, blockTimeMs, deposits[i], wallets)
				appendedSecond := appendAptosObservedTransfer(&out, txID, version, blockTimeMs, deposits[j], wallets)
				if !appendedFirst && !appendedSecond {
					logAptosUnwatchedDeposit(txID, version, deposits[i], "split")
					logAptosUnwatchedDeposit(txID, version, deposits[j], "split")
					continue
				}
				withdrawals[matchKey]--
				usedDeposits[i] = true
				usedDeposits[j] = true
				break
			}
		}
	}
	return out, nil
}

func appendAptosObservedTransfer(out *[]MoveObservedTransfer, txID string, version int64, blockTimeMs int64, event aptosFungibleEvent, wallets map[string]struct{}) bool {
	recipient := event.owner
	if !moveWalletWatched(recipient, wallets) {
		return false
	}
	*out = append(*out, MoveObservedTransfer{
		Network:        mdb.NetworkAptos,
		ReceiveAddress: recipient,
		Token:          NormalizeAptosPaymentSymbol(event.token.Symbol),
		RawAmount:      event.rawAmount,
		Decimals:       event.token.Decimals,
		MinAmount:      event.token.MinAmount,
		Amount:         rawToFloat(event.rawAmount, event.token.Decimals),
		TxID:           normalizeAptosTxID(txID),
		BlockTimeMs:    blockTimeMs,
		Version:        version,
		TransferKey:    moveAptosTransferKey(version, event.eventIndex, recipient, event.token.Symbol, event.rawAmount),
	})
	return true
}

func logAptosUnwatchedDeposit(txID string, version int64, event aptosFungibleEvent, matchMode string) {
	log.Sugar.Debugf(
		"[APTOS] matched deposit skipped owner_not_watched mode=%s version=%d tx=%s token=%s amount=%.8f raw=%s owner=%s",
		matchMode,
		version,
		normalizeAptosTxID(txID),
		NormalizeAptosPaymentSymbol(event.token.Symbol),
		rawToFloat(event.rawAmount, event.token.Decimals),
		event.rawAmount.String(),
		event.owner,
	)
}

func aptosDepositPairCanMatch(first, second aptosFungibleEvent) bool {
	if first.token == nil || second.token == nil || first.rawAmount == nil || second.rawAmount == nil {
		return false
	}
	if NormalizeAptosPaymentSymbol(first.token.Symbol) != NormalizeAptosPaymentSymbol(second.token.Symbol) {
		return false
	}
	return first.token.ContractAddress == second.token.ContractAddress
}

type aptosFungibleEvent struct {
	eventIndex int
	action     string
	owner      string
	rawAmount  *big.Int
	token      *mdb.ChainToken
}

func buildAptosFungibleEvents(tx gjson.Result, ctx aptosTransferContext, tokens []mdb.ChainToken) []aptosFungibleEvent {
	out := make([]aptosFungibleEvent, 0)
	for eventIndex, event := range tx.Get("events").Array() {
		action := aptosFungibleEventAction(event)
		if action == "" {
			continue
		}
		rawAmount, ok := parseSignedBigInt(aptosEventRawAmount(event, ctx))
		if !ok || rawAmount.Sign() <= 0 {
			continue
		}
		token := resolveAptosPaymentToken(aptosEventAssetID(event, ctx), tokens)
		if token == nil {
			continue
		}
		out = append(out, aptosFungibleEvent{
			eventIndex: eventIndex,
			action:     action,
			owner:      aptosEventRecipient(event, ctx),
			rawAmount:  rawAmount,
			token:      token,
		})
	}
	return out
}

func aptosFungibleEventAction(event gjson.Result) string {
	typ := strings.ToLower(strings.TrimSpace(event.Get("type").String()))
	switch typ {
	case "0x1::fungible_asset::deposit":
		return "deposit"
	case "0x1::fungible_asset::withdraw":
		return "withdraw"
	default:
		return ""
	}
}

func aptosTransferMatchKey(symbol string, rawAmount *big.Int) string {
	raw := ""
	if rawAmount != nil {
		raw = rawAmount.String()
	}
	return NormalizeAptosPaymentSymbol(symbol) + ":" + raw
}

func resolveAptosPaymentToken(assetID string, tokens []mdb.ChainToken) *mdb.ChainToken {
	return resolveMoveToken(mdb.NetworkAptos, assetID, tokens)
}

func NormalizeAptosPaymentSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

type aptosTransferContext struct {
	storeOwners   map[string]string
	storeMetadata map[string]string
}

func buildAptosTransferContext(tx gjson.Result) aptosTransferContext {
	ctx := aptosTransferContext{
		storeOwners:   make(map[string]string),
		storeMetadata: make(map[string]string),
	}
	for _, change := range tx.Get("changes").Array() {
		if change.Get("type").String() != "write_resource" {
			continue
		}
		store, err := addressutil.NormalizeMoveAddress(change.Get("address").String())
		if err != nil {
			continue
		}
		changeType := strings.TrimSpace(change.Get("data.type").String())
		switch changeType {
		case "0x1::fungible_asset::FungibleStore":
			if metadata := firstNonEmpty(
				change.Get("data.data.metadata.inner").String(),
				change.Get("data.data.metadata").String(),
			); metadata != "" {
				ctx.storeMetadata[store] = normalizeAptosObservedAssetID(metadata)
			}
		case "0x1::object::ObjectCore":
			if owner := firstMoveAddress(change.Get("data.data.owner").String()); owner != "" {
				ctx.storeOwners[store] = owner
			}
		}
	}
	return ctx
}

func aptosEventRecipient(event gjson.Result, ctx aptosTransferContext) string {
	if store := firstMoveAddress(event.Get("data.store").String()); store != "" {
		if owner := ctx.storeOwners[store]; owner != "" {
			return owner
		}
	}
	return ""
}

func aptosEventRawAmount(event gjson.Result, ctx aptosTransferContext) string {
	return event.Get("data.amount").String()
}

func aptosEventAssetID(event gjson.Result, ctx aptosTransferContext) string {
	if store := firstMoveAddress(event.Get("data.store").String()); store != "" {
		if metadata := ctx.storeMetadata[store]; metadata != "" {
			return metadata
		}
	}
	return ""
}

func normalizeAptosObservedAssetID(assetID string) string {
	return addressutil.NormalizeMoveAssetID(assetID)
}

func parseAptosTimestampMs(value gjson.Result) int64 {
	raw := strings.TrimSpace(value.String())
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n / 1000
}

func normalizeAptosTxID(txID string) string {
	txID = strings.ToLower(strings.TrimSpace(txID))
	if strings.HasPrefix(txID, "0x") {
		return txID
	}
	return "0x" + txID
}

func ValidateManualAptosPayment(order *mdb.Orders, txID string) (string, error) {
	txID = normalizeAptosTxID(txID)
	nodes, err := data.ListManualPaymentRpcCandidates(mdb.NetworkAptos, mdb.RpcNodeTypeHttp)
	if err != nil {
		return "", err
	}
	if len(nodes) == 0 {
		return "", fmt.Errorf("no enabled %s %s RPC node configured in rpc_nodes", mdb.NetworkAptos, mdb.RpcNodeTypeHttp)
	}

	var verifyErrors []string
	for _, node := range nodes {
		if strings.TrimSpace(node.Url) == "" {
			continue
		}
		if _, err = validateManualAptosPaymentWithNode(order, txID, node); err != nil {
			verifyErrors = append(verifyErrors, fmt.Sprintf("%s: %v", data.RpcNodeLogLabel(node), err))
			continue
		}
		return txID, nil
	}
	if len(verifyErrors) > 0 {
		return "", fmt.Errorf("manual Aptos verification failed: %s", strings.Join(verifyErrors, "; "))
	}
	return "", fmt.Errorf("no enabled %s %s RPC node configured in rpc_nodes", mdb.NetworkAptos, mdb.RpcNodeTypeHttp)
}

func validateManualAptosPaymentWithNode(order *mdb.Orders, txID string, node mdb.RpcNode) (string, error) {
	txID = normalizeAptosTxID(txID)
	body, err := aptosGetWithNode(node, fmt.Sprintf("/v1/transactions/by_hash/%s", txID))
	if err != nil {
		return "", fmt.Errorf("fetch aptos transaction: %w", err)
	}
	if err = validateManualAptosTransactionWithConfirm(order, txID, body, func(version int64) error {
		return ensureAptosTransferConfirmedWithNode(version, node)
	}); err != nil {
		return "", err
	}
	return txID, nil
}

func validateManualAptosTransaction(order *mdb.Orders, txID string, body []byte) error {
	return validateManualAptosTransactionWithConfirm(order, txID, body, EnsureAptosTransferConfirmed)
}

func validateManualAptosTransactionWithConfirm(order *mdb.Orders, txID string, body []byte, confirm func(int64) error) error {
	tokens, err := data.ListEnabledChainTokensByNetwork(mdb.NetworkAptos)
	if err != nil {
		return err
	}
	transfers, err := ParseAptosTransfers([]byte("["+string(body)+"]"), order.ReceiveAddress, tokens)
	if err != nil {
		return err
	}
	amountMismatch := false
	for _, transfer := range transfers {
		if !strings.EqualFold(transfer.TxID, txID) {
			continue
		}
		if err = confirm(transfer.Version); err != nil {
			return err
		}
		if err = EnsureMoveTransferMatchesOrder(order, transfer); err == nil {
			return nil
		}
		if strings.Contains(err.Error(), "amount mismatch") {
			amountMismatch = true
		} else {
			return err
		}
	}
	if amountMismatch {
		return fmt.Errorf("transaction amount mismatch")
	}
	return fmt.Errorf("matching aptos transfer to order address not found")
}

func aptosGet(path string) (body []byte, err error) {
	network := mdb.NetworkAptos
	defer recordMoveRPCResult(network, &err)

	tried := make([]uint64, 0, 3)
	var lastErr error
	for attempts := 0; attempts < 3; attempts++ {
		node, resolveErr := resolveAptosRpcNode(tried...)
		if resolveErr != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, resolveErr
		}
		if len(tried) > 0 {
			log.Sugar.Warnf("[APTOS] trying alternate RPC node path=%s node=%s", path, data.RpcNodeLogLabel(*node))
		}

		body, err = aptosGetWithNode(*node, path)
		if err == nil {
			data.RecordRpcNodeSuccess(node.ID)
			return body, nil
		}

		lastErr = err
		failures, cooling := data.RecordRpcNodeFailure(node.ID)
		nodeLabel := data.RpcNodeLogLabel(*node)
		if cooling {
			log.Sugar.Warnf("[APTOS] RPC node reached fail threshold path=%s node=%s failures=%d/%d", path, nodeLabel, failures, data.RpcFailoverThreshold)
		} else {
			log.Sugar.Warnf("[APTOS] RPC node failed path=%s node=%s failures=%d/%d", path, nodeLabel, failures, data.RpcFailoverThreshold)
		}
		tried = append(tried, node.ID)
	}
	return nil, lastErr
}

func resolveAptosRpcNode(excludeIDs ...uint64) (*mdb.RpcNode, error) {
	node, err := data.SelectGeneralRpcNode(mdb.NetworkAptos, mdb.RpcNodeTypeHttp, excludeIDs...)
	if err != nil {
		return nil, err
	}
	if node == nil || node.ID == 0 {
		return nil, fmt.Errorf("no enabled %s %s RPC node configured in rpc_nodes", mdb.NetworkAptos, mdb.RpcNodeTypeHttp)
	}
	rpcURL := strings.TrimSpace(node.Url)
	if rpcURL == "" {
		return nil, fmt.Errorf("rpc_nodes id=%d has empty url", node.ID)
	}
	node.Url = rpcURL
	return node, nil
}

func aptosGetWithNode(node mdb.RpcNode, path string) (body []byte, err error) {
	endpoint, err := aptosJoinURL(node.Url, path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: manualVerifyRequestTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("aptos rpc HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func aptosJoinURL(base, path string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", fmt.Errorf("empty aptos rpc url")
	}
	if strings.TrimSpace(path) == "" {
		return strings.TrimRight(base, "/"), nil
	}
	path = strings.TrimSpace(path)
	trimmedBase := strings.TrimRight(base, "/")
	lowerBase := strings.ToLower(trimmedBase)
	if strings.HasSuffix(lowerBase, "/v1") && strings.HasPrefix(path, "/v1") {
		path = strings.TrimPrefix(path, "/v1")
		if path == "" {
			path = "/"
		}
	}
	return trimmedBase + path, nil
}

func EnsureAptosTransferConfirmed(version int64) error {
	if version <= 0 {
		return fmt.Errorf("aptos transaction version missing")
	}
	latest, err := AptosGetLedgerVersion()
	if err != nil {
		return fmt.Errorf("fetch aptos latest ledger version: %w", err)
	}
	return ensureAptosConfirmations(version, latest)
}

func ensureAptosTransferConfirmedWithNode(version int64, node mdb.RpcNode) error {
	if version <= 0 {
		return fmt.Errorf("aptos transaction version missing")
	}
	body, err := aptosGetWithNode(node, "/v1")
	if err != nil {
		return fmt.Errorf("fetch aptos latest ledger version: %w", err)
	}
	latest := gjson.GetBytes(body, "ledger_version").Int()
	return ensureAptosConfirmations(version, latest)
}

func ensureAptosConfirmations(version, latest int64) error {
	chain, err := data.GetChainByNetwork(mdb.NetworkAptos)
	if err != nil {
		return err
	}
	minConfirmations := int64(1)
	if chain != nil && chain.MinConfirmations > 0 {
		minConfirmations = int64(chain.MinConfirmations)
	}
	if latest-version+1 < minConfirmations {
		return fmt.Errorf("aptos transaction confirmations insufficient")
	}
	return nil
}
