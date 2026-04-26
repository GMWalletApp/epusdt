package service

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"

	tron "github.com/GMWalletApp/epusdt/crypto"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/notify"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/http_client"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/math"
	"github.com/dromara/carbon/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gookit/goutil/stdutil"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

// resolveTronNode returns (baseURL, apiKey) for the TRON HTTP RPC node.
// It reads the first healthy (or any enabled) row from the rpc_nodes table.
func resolveTronNode() (string, string, error) {
	node, err := data.SelectRpcNode(mdb.NetworkTron, mdb.RpcNodeTypeHttp)
	if err != nil {
		return "", "", err
	}
	if node == nil || node.ID == 0 {
		return "", "", fmt.Errorf("no enabled %s %s RPC node configured in rpc_nodes", mdb.NetworkTron, mdb.RpcNodeTypeHttp)
	}
	rpcURL := strings.TrimRight(strings.TrimSpace(node.Url), "/")
	if rpcURL == "" {
		return "", "", fmt.Errorf("rpc_nodes id=%d has empty url", node.ID)
	}
	return rpcURL, node.ApiKey, nil
}

func Trc20CallBack(address string, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Error(err)
		}
	}()

	var innerWg sync.WaitGroup
	innerWg.Add(2)
	go checkTrxTransfers(address, &innerWg)
	go checkTrc20Transfers(address, &innerWg)
	innerWg.Wait()
}

func checkTrxTransfers(address string, wg *sync.WaitGroup) {
	defer wg.Done()

	// Native TRX is gated by a chain_tokens row with empty
	// contract_address and symbol=TRX. Admin can disable this row to
	// stop scanning native transfers without touching the chain toggle.
	trxCfg, err := data.GetEnabledChainTokenBySymbol(mdb.NetworkTron, "TRX")
	if err != nil {
		log.Sugar.Errorf("[TRX][%s] load chain_tokens err=%v", address, err)
		return
	}
	if trxCfg == nil || trxCfg.ID == 0 {
		log.Sugar.Debugf("[TRX][%s] native TRX disabled in chain_tokens, skipping", address)
		return
	}
	trxDecimals := trxCfg.Decimals
	if trxDecimals <= 0 {
		trxDecimals = 6
	}

	client := http_client.GetHttpClient()
	startTime := carbon.Now().AddHours(-24).TimestampMilli()
	endTime := carbon.Now().TimestampMilli()
	tronBaseURL, tronAPIKey, err := resolveTronNode()
	if err != nil {
		log.Sugar.Errorf("[TRX][%s] resolve rpc_nodes err=%v", address, err)
		return
	}
	url := fmt.Sprintf("%s/v1/accounts/%s/transactions", tronBaseURL, address)

	resp, err := client.R().SetQueryParams(map[string]string{
		"order_by":      "block_timestamp,desc",
		"limit":         "100",
		"only_to":       "true",
		"min_timestamp": stdutil.ToString(startTime),
		"max_timestamp": stdutil.ToString(endTime),
	}).SetHeader("TRON-PRO-API-KEY", tronAPIKey).Get(url)
	if err != nil {
		log.Sugar.Errorf("[TRX][%s] HTTP request failed: %v", address, err)
		return
	}
	if resp.StatusCode() != http.StatusOK {
		log.Sugar.Errorf("[TRX][%s] API returned status %d", address, resp.StatusCode())
		return
	}

	success := gjson.GetBytes(resp.Body(), "success").Bool()
	if !success {
		log.Sugar.Errorf("[TRX][%s] API response indicates failure", address)
		return
	}

	transfers := gjson.GetBytes(resp.Body(), "data").Array()
	if len(transfers) == 0 {
		log.Sugar.Debugf("[TRX][%s] no transfer records found", address)
		return
	}
	log.Sugar.Debugf("[TRX][%s] fetched %d transfer records", address, len(transfers))

	for i, transfer := range transfers {
		if transfer.Get("raw_data.contract.0.type").String() != "TransferContract" {
			continue
		}
		if transfer.Get("ret.0.contractRet").String() != "SUCCESS" {
			continue
		}

		toAddressHex := transfer.Get("raw_data.contract.0.parameter.value.to_address").String()
		toBytes, err := hex.DecodeString(toAddressHex)
		if err != nil {
			log.Sugar.Errorf("[TRX][%s] decode address failed on tx #%d: %v", address, i, err)
			continue
		}
		if tron.EncodeCheck(toBytes) != address {
			continue
		}

		rawAmount := transfer.Get("raw_data.contract.0.parameter.value.amount").String()
		decimalQuant, err := decimal.NewFromString(rawAmount)
		if err != nil {
			log.Sugar.Errorf("[TRX][%s] parse amount failed on tx #%d: %v", address, i, err)
			continue
		}
		amount := math.MustParsePrecFloat64(decimalQuant.Div(decimal.New(1, int32(trxDecimals))).InexactFloat64(), 2)
		if trxCfg.MinAmount > 0 && amount < trxCfg.MinAmount {
			continue
		}
		if amount <= 0 {
			continue
		}

		txID := transfer.Get("txID").String()
		tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, address, strings.ToUpper(strings.TrimSpace(trxCfg.Symbol)), amount)
		if err != nil {
			log.Sugar.Errorf("[TRX][%s] lookup trade_id failed hash=%s err=%v", address, txID, err)
			continue
		}
		if tradeID == "" {
			log.Sugar.Debugf("[TRX][%s] skip unmatched tx hash=%s amount=%.2f", address, txID, amount)
			continue
		}
		log.Sugar.Infof("[TRX][%s] matched trade_id=%s hash=%s amount=%.2f", address, tradeID, txID, amount)

		order, err := data.GetOrderInfoByTradeId(tradeID)
		if err != nil {
			log.Sugar.Errorf("[TRX][%s] get order failed trade_id=%s err=%v", address, tradeID, err)
			continue
		}
		blockTimestamp := transfer.Get("block_timestamp").Int()
		createTime := order.CreatedAt.TimestampMilli()
		if blockTimestamp < createTime {
			log.Sugar.Warnf("[TRX][%s] skip tx %s because block time %d is before order create time %d", address, txID, blockTimestamp, createTime)
			continue
		}

		req := &request.OrderProcessingRequest{
			ReceiveAddress:     address,
			Token:              strings.ToUpper(strings.TrimSpace(trxCfg.Symbol)),
			Network:            mdb.NetworkTron,
			TradeId:            tradeID,
			Amount:             amount,
			BlockTransactionId: txID,
		}
		err = OrderProcessing(req)
		if err != nil {
			if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
				log.Sugar.Infof("[TRX][%s] skip resolved transfer trade_id=%s hash=%s err=%v", address, tradeID, txID, err)
				continue
			}
			log.Sugar.Errorf("[TRX][%s] order processing failed trade_id=%s hash=%s err=%v", address, tradeID, txID, err)
			continue
		}

		sendPaymentNotification(order)
		log.Sugar.Infof("[TRX][%s] payment processed trade_id=%s hash=%s", address, tradeID, txID)
	}
}

func checkTrc20Transfers(address string, wg *sync.WaitGroup) {
	defer wg.Done()

	// Build contract -> token map for the TRON network. If nothing is
	// configured, skip — preserves the previous behavior of only watching
	// admin-approved tokens.
	tokens, err := data.ListEnabledChainTokensByNetwork(mdb.NetworkTron)
	if err != nil {
		log.Sugar.Errorf("[TRC20][%s] load chain_tokens err=%v", address, err)
		return
	}
	if len(tokens) == 0 {
		log.Sugar.Debugf("[TRC20][%s] no enabled chain_tokens, skipping", address)
		return
	}
	contractTokens := make(map[string]*mdb.ChainToken, len(tokens))
	for i := range tokens {
		c := strings.TrimSpace(tokens[i].ContractAddress)
		if c == "" {
			continue
		}
		contractTokens[c] = &tokens[i]
	}

	client := http_client.GetHttpClient()
	startTime := carbon.Now().AddHours(-24).TimestampMilli()
	endTime := carbon.Now().TimestampMilli()
	tronBaseURL, tronAPIKey, err := resolveTronNode()
	if err != nil {
		log.Sugar.Errorf("[TRC20][%s] resolve rpc_nodes err=%v", address, err)
		return
	}
	url := fmt.Sprintf("%s/v1/accounts/%s/transactions/trc20", tronBaseURL, address)

	resp, err := client.R().SetQueryParams(map[string]string{
		"order_by":      "block_timestamp,desc",
		"limit":         "100",
		"only_to":       "true",
		"min_timestamp": stdutil.ToString(startTime),
		"max_timestamp": stdutil.ToString(endTime),
	}).SetHeader("TRON-PRO-API-KEY", tronAPIKey).Get(url)
	if err != nil {
		log.Sugar.Errorf("[TRC20][%s] HTTP request failed: %v", address, err)
		return
	}
	if resp.StatusCode() != http.StatusOK {
		log.Sugar.Errorf("[TRC20][%s] API returned status %d", address, resp.StatusCode())
		return
	}

	success := gjson.GetBytes(resp.Body(), "success").Bool()
	if !success {
		log.Sugar.Errorf("[TRC20][%s] API response indicates failure", address)
		return
	}

	transfers := gjson.GetBytes(resp.Body(), "data").Array()
	if len(transfers) == 0 {
		log.Sugar.Debugf("[TRC20][%s] no transfer records found", address)
		return
	}
	log.Sugar.Debugf("[TRC20][%s] fetched %d transfer records", address, len(transfers))

	for i, transfer := range transfers {
		contractAddr := transfer.Get("token_info.address").String()
		cfg, ok := contractTokens[contractAddr]
		if !ok {
			continue
		}
		if transfer.Get("to").String() != address {
			continue
		}
		tokenSym := strings.ToUpper(strings.TrimSpace(cfg.Symbol))

		valueStr := transfer.Get("value").String()
		decimalQuant, err := decimal.NewFromString(valueStr)
		if err != nil {
			log.Sugar.Errorf("[TRC20][%s] parse value failed on tx #%d: %v", address, i, err)
			continue
		}
		tokenDecimals := transfer.Get("token_info.decimals").Int()
		if tokenDecimals <= 0 {
			tokenDecimals = int64(cfg.Decimals)
		}
		amount := math.MustParsePrecFloat64(decimalQuant.Div(decimal.New(1, int32(tokenDecimals))).InexactFloat64(), 2)
		if cfg.MinAmount > 0 && amount < cfg.MinAmount {
			continue
		}
		if amount <= 0 {
			continue
		}

		txID := transfer.Get("transaction_id").String()
		tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, address, tokenSym, amount)
		if err != nil {
			log.Sugar.Errorf("[TRC20][%s] lookup trade_id failed hash=%s err=%v", address, txID, err)
			continue
		}
		if tradeID == "" {
			log.Sugar.Debugf("[TRC20][%s] skip unmatched %s tx hash=%s amount=%.2f", address, tokenSym, txID, amount)
			continue
		}
		log.Sugar.Infof("[TRC20][%s] matched %s trade_id=%s hash=%s amount=%.2f", address, tokenSym, tradeID, txID, amount)

		order, err := data.GetOrderInfoByTradeId(tradeID)
		if err != nil {
			log.Sugar.Errorf("[TRC20][%s] get order failed trade_id=%s err=%v", address, tradeID, err)
			continue
		}
		blockTimestamp := transfer.Get("block_timestamp").Int()
		createTime := order.CreatedAt.TimestampMilli()
		if blockTimestamp < createTime {
			log.Sugar.Warnf("[TRC20][%s] skip tx %s because block time %d is before order create time %d", address, txID, blockTimestamp, createTime)
			continue
		}

		req := &request.OrderProcessingRequest{
			ReceiveAddress:     address,
			Token:              tokenSym,
			Network:            mdb.NetworkTron,
			TradeId:            tradeID,
			Amount:             amount,
			BlockTransactionId: txID,
		}
		err = OrderProcessing(req)
		if err != nil {
			if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
				log.Sugar.Infof("[TRC20][%s] skip resolved transfer trade_id=%s hash=%s err=%v", address, tradeID, txID, err)
				continue
			}
			log.Sugar.Errorf("[TRC20][%s] order processing failed trade_id=%s hash=%s err=%v", address, tradeID, txID, err)
			continue
		}

		sendPaymentNotification(order)
		log.Sugar.Infof("[TRC20][%s] payment processed trade_id=%s hash=%s", address, tradeID, txID)
	}
}

func evmChainLogLabel(chainNetwork string) string {
	switch chainNetwork {
	case mdb.NetworkEthereum:
		return "ETH"
	case mdb.NetworkBsc:
		return "BSC"
	case mdb.NetworkPolygon:
		return "POLYGON"
	case mdb.NetworkPlasma:
		return "PLASMA"
	default:
		return "EVM"
	}
}

// TryProcessEvmERC20Transfer 处理各 EVM 链上代币的 Transfer 入账。
// 代币识别、符号和 decimals 全部从 chain_tokens 表动态查询 —
// 管理后台新增 token 即可立即生效，无需代码改动。
func TryProcessEvmERC20Transfer(chainNetwork string, contract common.Address, toAddr common.Address, rawValue *big.Int, txHash string, blockTsMs int64) {
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[%s-WS] TryProcessEvmERC20Transfer panic: %v", evmChainLogLabel(chainNetwork), err)
		}
	}()

	net := evmChainLogLabel(chainNetwork)
	token, err := data.GetEnabledChainTokenByContract(chainNetwork, contract.Hex())
	if err != nil {
		log.Sugar.Warnf("[%s-WS] chain_tokens lookup err=%v contract=%s", net, err, contract.Hex())
		return
	}
	if token == nil || token.ID == 0 {
		log.Sugar.Debugf("[%s-WS] skip unconfigured contract %s", net, contract.Hex())
		return
	}
	tokenSym := strings.ToUpper(strings.TrimSpace(token.Symbol))
	if tokenSym == "" {
		log.Sugar.Warnf("[%s-WS] chain_token id=%d has empty symbol", net, token.ID)
		return
	}
	decimals := token.Decimals
	if decimals <= 0 {
		decimals = 6
	}

	walletAddr := strings.ToLower(toAddr.Hex())
	if rawValue == nil || rawValue.Sign() <= 0 {
		log.Sugar.Infof("[%s-%s][%s] skip non-positive or nil amount", net, tokenSym, walletAddr)
		return
	}
	divisor := decimal.New(1, int32(decimals))
	decimalQuant := decimal.NewFromBigInt(rawValue, 0)
	amount := math.MustParsePrecFloat64(decimalQuant.Div(divisor).InexactFloat64(), 2)
	if token.MinAmount > 0 && amount < token.MinAmount {
		log.Sugar.Debugf("[%s-%s][%s] skip amount %.2f below min_amount %.2f", net, tokenSym, walletAddr, amount, token.MinAmount)
		return
	}
	if amount <= 0 {
		log.Sugar.Warnf("[%s-%s][%s] skip non-positive amount %.2f", net, tokenSym, walletAddr, amount)
		return
	}

	log.Sugar.Debugf("[%s-%s][%s] processing transfer hash=%s amount=%.2f", net, tokenSym, walletAddr, txHash, amount)

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(chainNetwork, walletAddr, tokenSym, amount)
	if err != nil {
		log.Sugar.Warnf("[%s-%s][%s] lock lookup: %v", net, tokenSym, walletAddr, err)
		return
	}
	if tradeID == "" {
		log.Sugar.Warnf("[%s-%s][%s] skip unmatched tx hash=%s amount=%.2f", net, tokenSym, walletAddr, txHash, amount)
		return
	}

	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Warnf("[%s-%s][%s] load order: %v", net, tokenSym, walletAddr, err)
		return
	}
	if strings.ToLower(strings.TrimSpace(order.Network)) != chainNetwork {
		log.Sugar.Warnf("[%s-%s][%s] skip trade_id=%s network=%q", net, tokenSym, walletAddr, tradeID, order.Network)
		return
	}
	if strings.ToUpper(strings.TrimSpace(order.Token)) != tokenSym {
		log.Sugar.Warnf("[%s-%s][%s] skip trade_id=%s token mismatch order=%s", net, tokenSym, walletAddr, tradeID, order.Token)
		return
	}
	if blockTsMs > 0 && blockTsMs < order.CreatedAt.TimestampMilli() {
		log.Sugar.Warnf("[%s-%s][%s] skip tx %s because block_time_ms=%d is before order created_ms=%d",
			net, tokenSym, walletAddr, txHash, blockTsMs, order.CreatedAt.TimestampMilli())
		return
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     walletAddr,
		Token:              tokenSym,
		Network:            chainNetwork,
		TradeId:            tradeID,
		Amount:             amount,
		BlockTransactionId: txHash,
	}
	err = OrderProcessing(req)
	if err != nil {
		if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
			log.Sugar.Infof("[%s-%s][%s] skip resolved trade_id=%s hash=%s err=%v", net, tokenSym, walletAddr, tradeID, txHash, err)
			return
		}
		log.Sugar.Errorf("[%s-%s][%s] OrderProcessing: %v", net, tokenSym, walletAddr, err)
		return
	}

	sendPaymentNotification(order)
	log.Sugar.Infof("[%s-%s][%s] payment processed trade_id=%s hash=%s", net, tokenSym, walletAddr, tradeID, txHash)
}

func sendPaymentNotification(order *mdb.Orders) {
	if order == nil {
		return
	}
	latest := order
	if order.TradeId != "" {
		refreshed, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil {
			log.Sugar.Warnf("[notify] reload order failed trade_id=%s err=%v", order.TradeId, err)
		} else if refreshed != nil {
			latest = refreshed
		}
	}
	msg := fmt.Sprintf(
		"🎉 <b>收款成功通知</b>\n\n"+
			"💰 <b>金额信息</b>\n"+
			"├ 订单金额：<code>%.2f %s</code>\n"+
			"└ 实际到账：<code>%.2f %s</code>\n\n"+
			"📋 <b>订单信息</b>\n"+
			"├ 交易号：<code>%s</code>\n"+
			"├ 订单号：<code>%s</code>\n"+
			"├ 网络：<code>%s</code>\n"+
			"└ 钱包地址：<code>%s</code>\n\n"+
			"⏰ <b>时间信息</b>\n"+
			"├ 创建时间：%s\n"+
			"└ 支付时间：%s",
		latest.Amount,
		strings.ToUpper(latest.Currency),
		latest.ActualAmount,
		strings.ToUpper(latest.Token),
		latest.TradeId,
		latest.OrderId,
		networkDisplay(latest.Network),
		latest.ReceiveAddress,
		latest.CreatedAt.ToDateTimeString(),
		latest.UpdatedAt.ToDateTimeString(),
	)
	notify.Dispatch(mdb.NotifyEventPaySuccess, msg)
}

func networkDisplay(n string) string {
	switch strings.ToLower(strings.TrimSpace(n)) {
	case mdb.NetworkTron:
		return "Tron"
	case mdb.NetworkSolana:
		return "Solana"
	case mdb.NetworkEthereum:
		return "Ethereum"
	case mdb.NetworkBsc:
		return "BSC"
	case mdb.NetworkPolygon:
		return "Polygon"
	case mdb.NetworkPlasma:
		return "Plasma"
	default:
		if n == "" {
			return "Tron"
		}
		return strings.ToUpper(n)
	}
}
