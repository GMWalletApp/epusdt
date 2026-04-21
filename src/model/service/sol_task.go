package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/assimon/luuu/config"
	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/model/request"
	"github.com/assimon/luuu/util/constant"
	"github.com/assimon/luuu/util/log"
	"github.com/assimon/luuu/util/math"
	"github.com/gagliardetto/solana-go"
	"github.com/go-resty/resty/v2"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

// gProcessedSignatures 已处理签名缓存，避免重复调用 getTransaction
var gProcessedSignatures sync.Map // sig -> unix timestamp

type TransferInfo struct {
	Source      string  // Source address (for SOL) or source ATA (for SPL tokens)
	Destination string  // Destination address (for SOL) or destination ATA (for SPL tokens)
	Mint        string  // Token mint (e.g. USDT mint, USDC mint, or "SOL" for native transfers)
	Amount      float64 // Human-readable amount (adjusted for decimals)
	RawAmount   uint64  // Raw amount from the transaction (before adjusting for decimals)
	Decimals    *int    // Optional decimals from the transaction, if available
	BlockTime   int64   // Block time of the transfer unit is seconds since epoch
}

// SolCallBack 扫描指定钱包地址的 Solana 链上交易，匹配待支付订单并确认收款。
func SolCallBack(address string, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[SOL][%s] panic recovered: %v", address, err)
		}
	}()

	// Clean up old entries from processed cache (older than 1 hour)
	cleanupCutoff := time.Now().Add(-1 * time.Hour).Unix()
	gProcessedSignatures.Range(func(key, value interface{}) bool {
		if ts, ok := value.(int64); ok && ts < cleanupCutoff {
			gProcessedSignatures.Delete(key)
		}
		return true
	})

	limit := 1000

	// 查询钱包地址 + USDT ATA + USDC ATA 三个地址的签名
	queryAddrs := []string{address}
	if usdtAta, err := FindATAAddress(address, USDT_Mint); err == nil {
		queryAddrs = append(queryAddrs, usdtAta)
	} else {
		log.Sugar.Errorf("[SOL][%s] failed to derive USDT ATA: %v", address, err)
	}
	if usdcAta, err := FindATAAddress(address, USDC_Mint); err == nil {
		queryAddrs = append(queryAddrs, usdcAta)
	} else {
		log.Sugar.Errorf("[SOL][%s] failed to derive USDC ATA: %v", address, err)
	}

	// 时间截止线：订单过期时间 + 5 分钟
	cutoffTime := time.Now().Add(-config.GetOrderExpirationTimeDuration() - 5*time.Minute).Unix()

	// 拉取签名并去重
	seen := make(map[string]bool)
	var result []solSignatureResult
	for _, queryAddr := range queryAddrs {
		batch, err := collectSolSignaturesForAddress(queryAddr, limit, cutoffTime)
		if err != nil {
			log.Sugar.Errorf("[SOL][%s] collect signatures for %s failed: %v", address, queryAddr, err)
			continue
		}
		for _, sig := range batch {
			if !seen[sig.Signature] {
				seen[sig.Signature] = true
				result = append(result, sig)
			}
		}
	}

	if len(result) == 0 {
		log.Sugar.Debugf("[SOL][%s] no transaction signatures found", address)
		return
	}

	// 按 blockTime 降序排列
	sort.Slice(result, func(i, j int) bool {
		if result[i].BlockTime == nil {
			return false
		}
		if result[j].BlockTime == nil {
			return true
		}
		return *result[i].BlockTime > *result[j].BlockTime
	})

	log.Sugar.Debugf("[SOL][%s] fetched %d unique signatures from %d addresses, cutoff=%d",
		address, len(result), len(queryAddrs), cutoffTime)

	// Process each transaction signature
	for sigIdx, txSig := range result {
		sig := txSig.Signature

		// Skip failed transactions
		if txSig.Err != nil {
			continue
		}

		// 超过截止时间的旧签名不再处理
		if txSig.BlockTime != nil && *txSig.BlockTime < cutoffTime {
			log.Sugar.Debugf("[SOL][%s] [%d/%d] sig=%s blockTime=%d before cutoff=%d, stopping scan",
				address, sigIdx+1, len(result), sig, *txSig.BlockTime, cutoffTime)
			break
		}

		// 跳过已处理的签名
		if _, ok := gProcessedSignatures.Load(sig); ok {
			continue
		}

		log.Sugar.Debugf("[SOL][%s] [%d/%d] processing sig=%s slot=%d", address, sigIdx+1, len(result), sig, txSig.Slot)

		txData, err := SolGetTransaction(sig)
		if err != nil {
			log.Sugar.Debugf("[SOL][%s] sig=%s fetch failed: %v", address, sig, err)
			continue
		}

		instructions := gjson.GetBytes(txData, "result.transaction.message.instructions").Array()
		log.Sugar.Debugf("[SOL][%s] sig=%s has %d instructions", address, sig, len(instructions))

		for instrIdx, instruction := range instructions {
			programID := instruction.Get("programId").String()
			parsedType := instruction.Get("parsed.type").String()
			log.Sugar.Debugf("[SOL][%s] sig=%s instr[%d] programId=%s parsedType=%s", address, sig, instrIdx, programID, parsedType)

			transferInfo, err := ParseTransferInfoFromInstruction(instruction, txData)
			if err != nil {
				log.Sugar.Debugf("[SOL][%s] sig=%s instr[%d] parse error: %v", address, sig, instrIdx, err)
				continue
			}
			if transferInfo == nil {
				continue
			}

			log.Sugar.Debugf("[SOL][%s] sig=%s instr[%d] transfer: src=%s dst=%s mint=%s rawAmount=%d amount=%.6f blockTime=%d",
				address, sig, instrIdx,
				transferInfo.Source, transferInfo.Destination, transferInfo.Mint,
				transferInfo.RawAmount, transferInfo.Amount, transferInfo.BlockTime)

			if !isTransferToAddress(transferInfo, address) {
				continue
			}

			token, amount := getTokenTypeAndAmount(transferInfo)
			if token == "" || amount <= 0 {
				continue
			}

			log.Sugar.Infof("[SOL][%s] sig=%s instr[%d] incoming transfer confirmed: token=%s amount=%.2f -> querying transaction_lock network=solana address=%s token=%s amount=%.2f",
				address, sig, instrIdx, token, amount, address, token, amount)

			tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkSolana, address, token, amount)
			if err != nil {
				log.Sugar.Errorf("[SOL][%s] sig=%s query transaction_lock failed: %v", address, sig, err)
				continue
			}
			if tradeID == "" {
				log.Sugar.Infof("[SOL][%s] sig=%s no active transaction_lock matched: network=solana address=%s token=%s amount=%.2f (no order or expired)",
					address, sig, address, token, amount)
				continue
			}
			log.Sugar.Infof("[SOL][%s] transaction_lock matched: trade_id=%s sig=%s token=%s amount=%.2f",
				address, tradeID, sig, token, amount)

			order, err := data.GetOrderInfoByTradeId(tradeID)
			if err != nil {
				log.Sugar.Errorf("[SOL][%s] sig=%s load order failed for trade_id=%s: %v", address, sig, tradeID, err)
				continue
			}
			log.Sugar.Infof("[SOL][%s] order loaded: trade_id=%s order_id=%s status=%d created_at_ms=%d",
				address, tradeID, order.OrderId, order.Status, order.CreatedAt.TimestampMilli())

			// blockTime 秒 → 毫秒，与订单创建时间对齐
			blockTimestamp := transferInfo.BlockTime * 1000
			createTime := order.CreatedAt.TimestampMilli()
			log.Sugar.Infof("[SOL][%s] time check: sig=%s block_time_ms=%d order_created_ms=%d diff_ms=%d",
				address, sig, blockTimestamp, createTime, blockTimestamp-createTime)
			if blockTimestamp < createTime {
				log.Sugar.Warnf("[SOL][%s] sig=%s skipped: block_time_ms=%d is %d ms before order created_ms=%d (transaction predates the order)",
					address, sig, blockTimestamp, createTime-blockTimestamp, createTime)
				continue
			}

			req := &request.OrderProcessingRequest{
				ReceiveAddress:     address,
				Token:              token,
				Network:            mdb.NetworkSolana,
				TradeId:            tradeID,
				Amount:             amount,
				BlockTransactionId: sig,
			}
			log.Sugar.Infof("[SOL][%s] calling OrderProcessing: trade_id=%s sig=%s token=%s amount=%.2f",
				address, tradeID, sig, token, amount)
			err = OrderProcessing(req)
			if err != nil {
				if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
					log.Sugar.Infof("[SOL][%s] sig=%s already resolved: trade_id=%s reason=%v", address, sig, tradeID, err)
					continue
				}
				log.Sugar.Errorf("[SOL][%s] sig=%s OrderProcessing failed for trade_id=%s: %v", address, sig, tradeID, err)
				continue
			}

			log.Sugar.Infof("[SOL][%s] order marked paid: trade_id=%s sig=%s token=%s amount=%.2f, sending telegram notification",
				address, tradeID, sig, token, amount)
			sendPaymentNotification(order)
			log.Sugar.Infof("[SOL][%s] payment fully processed: trade_id=%s sig=%s", address, tradeID, sig)
		}

		// 标记已处理
		gProcessedSignatures.Store(sig, time.Now().Unix())
	}
}

// solSignatureResult getSignaturesForAddress 返回结构
type solSignatureResult struct {
	Signature string      `json:"signature"`
	Slot      uint64      `json:"slot"`
	Err       interface{} `json:"err"`
	BlockTime *int64      `json:"blockTime"`
}

func collectSolSignaturesForAddress(address string, limit int, cutoffTime int64) ([]solSignatureResult, error) {
	return collectSolSignaturesForAddressWithFetcher(SolGetSignaturesForAddress, address, limit, cutoffTime)
}

func collectSolSignaturesForAddressWithFetcher(fetcher func(string, int, string, string) ([]byte, error), address string, limit int, cutoffTime int64) ([]solSignatureResult, error) {
	beforeSig := ""
	result := make([]solSignatureResult, 0, limit)
	for {
		respBody, err := fetcher(address, limit, "", beforeSig)
		if err != nil {
			return nil, err
		}

		resultBody := gjson.GetBytes(respBody, "result")
		if !resultBody.Exists() || !resultBody.IsArray() {
			return nil, fmt.Errorf("unexpected response format for %s: %s", address, string(respBody))
		}

		var batch []solSignatureResult
		if err = json.Unmarshal([]byte(resultBody.Raw), &batch); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return result, nil
		}

		stop := false
		for _, sig := range batch {
			if sig.BlockTime != nil && *sig.BlockTime < cutoffTime {
				stop = true
				break
			}
			result = append(result, sig)
		}
		if stop || len(batch) < limit {
			return result, nil
		}

		beforeSig = batch[len(batch)-1].Signature
		if beforeSig == "" {
			return result, nil
		}
	}
}

// SolRetryClient 发送 Solana JSON-RPC 请求，自动重试
func SolRetryClient(method string, params []interface{}) ([]byte, error) {
	client := resty.New()
	client.SetRetryCount(5)
	client.SetRetryWaitTime(2 * time.Second)
	client.SetRetryMaxWaitTime(10 * time.Second)
	client.AddRetryCondition(func(r *resty.Response, err error) bool {
		if err != nil {
			return true
		}
		if r.StatusCode() >= 429 || r.StatusCode() >= 500 {
			return true
		}
		return false
	})

	rpcUrl := config.GetSolanaRpcUrl()

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  method,
			"params":  params,
		}).
		Post(rpcUrl)
	if err != nil {
		return nil, err
	}

	respBody := resp.Body()

	return respBody, nil
}

func SolGetSignaturesForAddress(address string, limit int, untilSig string, beforeSig string) ([]byte, error) {
	opts := map[string]interface{}{
		"commitment": "finalized",
		"limit":      limit,
	}
	if untilSig != "" {
		opts["until"] = untilSig
	}
	if beforeSig != "" {
		opts["before"] = beforeSig
	}

	bodyData, err := SolRetryClient("getSignaturesForAddress",
		[]interface{}{address, opts})
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	err = json.Unmarshal(bodyData, &result)
	if err != nil {
		return nil, err
	}

	_, ok := result["result"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response format: %v", result)
	}

	return bodyData, nil
}

func SolGetTransaction(sig string) ([]byte, error) {
	txData, err := SolRetryClient("getTransaction", []interface{}{
		sig,
		map[string]interface{}{
			"encoding":                       "jsonParsed",
			"commitment":                     "confirmed",
			"maxSupportedTransactionVersion": 0, // suport
		},
	})
	if err != nil {
		log.Sugar.Errorf("SolRetryClient failed: %v", err)
		return nil, err
	}

	errField := gjson.GetBytes(txData, "result.meta.err")
	if errField.Exists() && errField.Type != gjson.Null {
		log.Sugar.Warnf("Transaction failed: %v", errField.String())
		return nil, fmt.Errorf("transaction failed: %s", errField.String())
	}

	return txData, nil
}

// isTransferToAddress 判断转账目标是否为指定钱包地址
func isTransferToAddress(transfer *TransferInfo, targetAddress string) bool {
	// Native SOL transfer - check destination directly
	if transfer.Mint == "SOL" {
		return strings.EqualFold(transfer.Destination, targetAddress)
	}

	// Skip Transfer instruction without mint info (use TransferChecked instead)
	if transfer.Mint == "" {
		return false
	}

	// SPL Token transfer - check if destination ATA matches
	return MatchAtaAddress(targetAddress, transfer.Mint, transfer.Destination)
}

// getTokenTypeAndAmount 根据 mint 识别代币类型并计算可读金额
func getTokenTypeAndAmount(transfer *TransferInfo) (string, float64) {
	mint := transfer.Mint

	// Native SOL
	if mint == "SOL" {
		return "SOL", transfer.Amount
	}

	// SPL Tokens
	switch mint {
	case USDT_Mint:
		decimals := USDT_Decimals
		if transfer.Decimals != nil {
			decimals = int(*transfer.Decimals)
		}
		return "USDT", ADJustAmount(transfer.RawAmount, decimals)

	case USDC_Mint:
		decimals := USDC_Decimals
		if transfer.Decimals != nil {
			decimals = int(*transfer.Decimals)
		}
		return "USDC", ADJustAmount(transfer.RawAmount, decimals)

	default:
		// Unsupported token
		return "", 0
	}
}

const (
	// Mint token
	USDT_Mint = "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
	USDC_Mint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

	USDT_Decimals = 6
	USDC_Decimals = 6
	SOL_Decimals  = 9
)

const (
	// SPL Token instructions
	InstructionTransfer        = 3
	InstructionTransferChecked = 12
)

const (
	// System Program
	SystemProgramID           = "11111111111111111111111111111111"
	InstructionSystemTransfer = 2
)

const (
	TokenProgramID     = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	Token2022ProgramID = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
)

// ADJustAmount 将链上原始金额转为可读金额（除以 10^decimals，保留 2 位小数）
func ADJustAmount(amount uint64, decimals int) float64 {
	if amount == 0 {
		return 0
	}
	decimalAmount := decimal.NewFromBigInt(new(big.Int).SetUint64(amount), 0)
	// 10^decimals
	decimalDivisor := decimal.New(1, int32(decimals))
	adjustedAmount := decimalAmount.Div(decimalDivisor)
	// Round to 2 decimal places
	return math.MustParsePrecFloat64(adjustedAmount.InexactFloat64(), 2)
}

func MatchUsdtAtaAddress(address string, ataTo string) bool {
	ata, err := FindATAAddress(address, USDT_Mint)
	if err != nil {
		return false
	}

	return strings.EqualFold(ata, ataTo)
}

func MatchUsdcAtaAddress(address string, ataTo string) bool {
	ata, err := FindATAAddress(address, USDC_Mint)
	if err != nil {
		return false
	}

	return strings.EqualFold(ata, ataTo)
}

func MatchAtaAddress(address string, mint string, ataTo string) bool {
	ata, err := FindATAAddress(address, mint)
	if err != nil {
		return false
	}

	return strings.EqualFold(ata, ataTo)
}

func FindATAAddress(owner, mint string) (string, error) {
	ownerPubKey, err := solana.PublicKeyFromBase58(owner)
	if err != nil {
		return "", fmt.Errorf("invalid owner public key: %w", err)
	}

	mintPubKey, err := solana.PublicKeyFromBase58(mint)
	if err != nil {
		return "", fmt.Errorf("invalid mint public key: %w", err)
	}

	ata, _, err := solana.FindAssociatedTokenAddress(ownerPubKey, mintPubKey)
	if err != nil {
		return "", fmt.Errorf("find associated token address failed: %w", err)
	}

	return ata.String(), nil
}

// ParseTransferInfoFromInstruction 从单条指令中解析转账信息，非转账指令返回 nil
func ParseTransferInfoFromInstruction(instruction gjson.Result, txData []byte) (*TransferInfo, error) {
	programID := instruction.Get("programId").String()
	parsedType := instruction.Get("parsed.type").String()

	if programID == SystemProgramID && parsedType == "transfer" {
		return parseSystemTransfer(instruction, txData)
	}

	if programID == TokenProgramID || programID == Token2022ProgramID {
		switch parsedType {
		case "transfer":
			return parseSplTransfer(instruction, txData)
		case "transferChecked":
			return parseSplTransferChecked(instruction, txData)
		}
	}

	// Skip non-transfer instructions (ComputeBudget, AToken create, etc.)
	return nil, nil
}

func parseSystemTransfer(instruction gjson.Result, txData []byte) (*TransferInfo, error) {
	info := instruction.Get("parsed.info")
	source := info.Get("source").String()
	destination := info.Get("destination").String()
	lamports := info.Get("lamports").Uint()
	blockTime := gjson.GetBytes(txData, "result.blockTime").Int()

	return &TransferInfo{
		Source:      source,
		Destination: destination,
		Mint:        "SOL",
		Amount:      ADJustAmount(lamports, SOL_Decimals),
		RawAmount:   lamports,
		BlockTime:   blockTime,
	}, nil
}

// parseSplTransfer 解析 SPL Token "transfer" 指令，mint 从 postTokenBalances 中查找
func parseSplTransfer(instruction gjson.Result, txData []byte) (*TransferInfo, error) {
	info := instruction.Get("parsed.info")
	source := info.Get("source").String()
	destination := info.Get("destination").String()
	amountStr := info.Get("amount").String()
	blockTime := gjson.GetBytes(txData, "result.blockTime").Int()

	rawAmount, ok := new(big.Int).SetString(amountStr, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount: %s", amountStr)
	}

	// Look up mint and decimals from postTokenBalances using the destination ATA
	mint, decimals, found := findMintFromTokenBalances(destination, txData)
	if !found {
		// Try source ATA as fallback
		mint, decimals, found = findMintFromTokenBalances(source, txData)
	}
	if !found {
		return nil, fmt.Errorf("could not determine mint for transfer: source=%s dest=%s", source, destination)
	}

	d := decimals
	return &TransferInfo{
		Source:      source,
		Destination: destination,
		Mint:        mint,
		Amount:      ADJustAmount(rawAmount.Uint64(), decimals),
		RawAmount:   rawAmount.Uint64(),
		Decimals:    &d,
		BlockTime:   blockTime,
	}, nil
}

// parseSplTransferChecked 解析 SPL Token "transferChecked" 指令
func parseSplTransferChecked(instruction gjson.Result, txData []byte) (*TransferInfo, error) {
	info := instruction.Get("parsed.info")
	source := info.Get("source").String()
	destination := info.Get("destination").String()
	mint := info.Get("mint").String()
	amountStr := info.Get("tokenAmount.amount").String()
	decimals := int(info.Get("tokenAmount.decimals").Int())
	blockTime := gjson.GetBytes(txData, "result.blockTime").Int()

	rawAmount, ok := new(big.Int).SetString(amountStr, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount: %s", amountStr)
	}

	return &TransferInfo{
		Source:      source,
		Destination: destination,
		Mint:        mint,
		Amount:      ADJustAmount(rawAmount.Uint64(), decimals),
		RawAmount:   rawAmount.Uint64(),
		Decimals:    &decimals,
		BlockTime:   blockTime,
	}, nil
}

// findMintFromTokenBalances 从 postTokenBalances 中查找 ATA 对应的 mint 和 decimals
func findMintFromTokenBalances(ataAddress string, txData []byte) (string, int, bool) {
	accountKeys := gjson.GetBytes(txData, "result.transaction.message.accountKeys").Array()
	accountIndex := -1
	for i, key := range accountKeys {
		if key.Get("pubkey").String() == ataAddress {
			accountIndex = i
			break
		}
	}
	if accountIndex == -1 {
		return "", 0, false
	}

	balances := gjson.GetBytes(txData, "result.meta.postTokenBalances").Array()
	for _, balance := range balances {
		if int(balance.Get("accountIndex").Int()) == accountIndex {
			mint := balance.Get("mint").String()
			decimals := int(balance.Get("uiTokenAmount.decimals").Int())
			return mint, decimals, true
		}
	}
	return "", 0, false
}
