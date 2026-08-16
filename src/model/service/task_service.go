package service

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/notify"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/math"
	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"
)

func resolveTronNode() (string, string, error) {
	node, err := ResolveTronRpcNode()
	if err != nil {
		return "", "", err
	}
	rpcURL := strings.TrimRight(strings.TrimSpace(node.Url), "/")
	return rpcURL, node.ApiKey, nil
}

func ResolveTronRpcNode(excludeIDs ...uint64) (*mdb.RpcNode, error) {
	node, err := data.SelectGeneralRpcNode(mdb.NetworkTron, mdb.RpcNodeTypeHttp, excludeIDs...)
	if err != nil {
		return nil, err
	}
	if node == nil || node.ID == 0 {
		return nil, fmt.Errorf("no enabled %s %s RPC node configured in rpc_nodes", mdb.NetworkTron, mdb.RpcNodeTypeHttp)
	}
	rpcURL := strings.TrimRight(strings.TrimSpace(node.Url), "/")
	if rpcURL == "" {
		return nil, fmt.Errorf("rpc_nodes id=%d has empty url", node.ID)
	}
	node.Url = rpcURL
	return node, nil
}

func ResolveTronNode() (string, string, error) {
	return resolveTronNode()
}

func TryProcessTronTRC20Transfer(token mdb.ChainToken, toAddr string, rawValue *big.Int, txHash string, blockTsMs int64) {
	tokenSym := strings.ToUpper(strings.TrimSpace(token.Symbol))
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[TRC20-%s][%s] TryProcessTronTRC20Transfer panic: %v", tokenSym, toAddr, err)
		}
	}()

	addr := strings.TrimSpace(toAddr)
	if tokenSym == "" || addr == "" || rawValue == nil || rawValue.Sign() <= 0 {
		return
	}
	decimals := token.Decimals
	if decimals < 0 {
		decimals = 0
	}

	decimalQuant := decimal.NewFromBigInt(rawValue, 0)
	amount := math.MustParsePrecFloat64(decimalQuant.Div(decimal.New(1, int32(decimals))).InexactFloat64(), data.MaxAmountPrecision)
	if amount <= 0 {
		return
	}
	if token.MinAmount > 0 && amount < token.MinAmount {
		log.Sugar.Debugf("[TRC20-%s][%s] skip below min amount hash=%s amount=%.2f min=%.2f", tokenSym, addr, txHash, amount, token.MinAmount)
		return
	}

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, addr, tokenSym, amount)
	if err != nil {
		log.Sugar.Warnf("[TRC20-%s][%s] lock lookup: %v", tokenSym, addr, err)
		return
	}
	if tradeID == "" {
		log.Sugar.Debugf("[TRC20-%s][%s] skip unmatched tx hash=%s amount=%.2f", tokenSym, addr, txHash, amount)
		return
	}

	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Warnf("[TRC20-%s][%s] load order: %v", tokenSym, addr, err)
		return
	}
	if strings.ToLower(strings.TrimSpace(order.Network)) != mdb.NetworkTron {
		log.Sugar.Warnf("[TRC20-%s][%s] skip trade_id=%s network=%q", tokenSym, addr, tradeID, order.Network)
		return
	}
	if strings.ToUpper(strings.TrimSpace(order.Token)) != tokenSym {
		log.Sugar.Warnf("[TRC20-%s][%s] skip trade_id=%s token mismatch order=%s", tokenSym, addr, tradeID, order.Token)
		return
	}
	if blockTsMs > 0 && blockTsMs < order.CreatedAt.TimestampMilli() {
		log.Sugar.Warnf("[TRC20-%s][%s] skip tx %s because block time %d is before order create time %d", tokenSym, addr, txHash, blockTsMs, order.CreatedAt.TimestampMilli())
		return
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     addr,
		Token:              tokenSym,
		Network:            mdb.NetworkTron,
		TradeId:            tradeID,
		Amount:             amount,
		BlockTransactionId: txHash,
	}
	err = OrderProcessing(req)
	if err != nil {
		if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
			log.Sugar.Infof("[TRC20-%s][%s] skip resolved transfer trade_id=%s hash=%s err=%v", tokenSym, addr, tradeID, txHash, err)
			return
		}
		log.Sugar.Errorf("[TRC20-%s][%s] OrderProcessing trade_id=%s hash=%s: %v", tokenSym, addr, tradeID, txHash, err)
		return
	}

	sendPaymentNotification(order)
	log.Sugar.Infof("[TRC20-%s][%s] payment processed trade_id=%s hash=%s", tokenSym, addr, tradeID, txHash)
}

func TryProcessTronTRXTransfer(toAddr string, rawSun int64, txHash string, blockTsMs int64) {
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[TRX][%s] TryProcessTronTRXTransfer panic: %v", toAddr, err)
		}
	}()

	addr := strings.TrimSpace(toAddr)
	if addr == "" || rawSun <= 0 {
		return
	}

	decimalQuant := decimal.NewFromInt(rawSun)
	amount := math.MustParsePrecFloat64(decimalQuant.Div(decimal.NewFromInt(1_000_000)).InexactFloat64(), data.MaxAmountPrecision)
	if amount <= 0 {
		return
	}

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, addr, "TRX", amount)
	if err != nil {
		log.Sugar.Warnf("[TRX][%s] lock lookup: %v", addr, err)
		return
	}
	if tradeID == "" {
		log.Sugar.Debugf("[TRX][%s] skip unmatched tx hash=%s amount=%.2f", addr, txHash, amount)
		return
	}

	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Warnf("[TRX][%s] load order: %v", addr, err)
		return
	}
	if blockTsMs > 0 && blockTsMs < order.CreatedAt.TimestampMilli() {
		log.Sugar.Warnf("[TRX][%s] skip tx %s because block time %d is before order create time %d", addr, txHash, blockTsMs, order.CreatedAt.TimestampMilli())
		return
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     addr,
		Token:              "TRX",
		Network:            mdb.NetworkTron,
		TradeId:            tradeID,
		Amount:             amount,
		BlockTransactionId: txHash,
	}
	err = OrderProcessing(req)
	if err != nil {
		if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
			log.Sugar.Infof("[TRX][%s] skip resolved transfer trade_id=%s hash=%s err=%v", addr, tradeID, txHash, err)
			return
		}
		log.Sugar.Errorf("[TRX][%s] OrderProcessing trade_id=%s hash=%s: %v", addr, tradeID, txHash, err)
		return
	}

	sendPaymentNotification(order)
	log.Sugar.Infof("[TRX][%s] payment processed trade_id=%s hash=%s", addr, tradeID, txHash)
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
	case mdb.NetworkBase:
		return "BASE"
	case mdb.NetworkArbitrum:
		return "ARBITRUM"
	default:
		return "EVM"
	}
}

func TryProcessEvmERC20Transfer(chainNetwork string, contract common.Address, toAddr common.Address, rawValue *big.Int, txHash string, blockTsMs int64) {
	if err := ProcessEvmERC20Transfer(chainNetwork, contract, toAddr, rawValue, txHash, blockTsMs); err != nil {
		log.Sugar.Errorf("[%s-WS] processing transfer failed hash=%s: %v", evmChainLogLabel(chainNetwork), txHash, err)
	}
}

func ProcessEvmERC20Transfer(chainNetwork string, contract common.Address, toAddr common.Address, rawValue *big.Int, txHash string, blockTsMs int64) (processErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			processErr = fmt.Errorf("处理 EVM 转账发生 panic: %v", recovered)
			log.Sugar.Errorf("[%s-WS] TryProcessEvmERC20Transfer panic: %v", evmChainLogLabel(chainNetwork), recovered)
		}
	}()

	net := evmChainLogLabel(chainNetwork)
	tokenConfig, err := data.GetEnabledChainTokenByContract(chainNetwork, contract.Hex())
	if err != nil {
		log.Sugar.Warnf("[%s-WS] load chain token contract=%s: %v", net, contract.Hex(), err)
		return fmt.Errorf("读取链代币配置 network=%s contract=%s: %w", chainNetwork, contract.Hex(), err)
	}
	if tokenConfig == nil || tokenConfig.ID == 0 {
		log.Sugar.Warnf("[%s-WS] skip unconfigured contract %s", net, contract.Hex())
		return fmt.Errorf("链代币配置已失效 network=%s contract=%s", chainNetwork, contract.Hex())
	}
	tokenSym := strings.ToUpper(strings.TrimSpace(tokenConfig.Symbol))
	if tokenSym == "" {
		log.Sugar.Warnf("[%s-WS] skip contract %s with empty token symbol", net, contract.Hex())
		return fmt.Errorf("链代币符号为空 network=%s contract=%s", chainNetwork, contract.Hex())
	}
	walletAddr := strings.ToLower(toAddr.Hex())
	if rawValue == nil || rawValue.Sign() <= 0 {
		log.Sugar.Infof("[%s-%s][%s] skip non-positive or nil amount", net, tokenSym, walletAddr)
		return
	}
	decimals := tokenConfig.Decimals
	if decimals < 0 {
		return fmt.Errorf("链代币精度无效 network=%s contract=%s decimals=%d", chainNetwork, contract.Hex(), decimals)
	}
	pow := decimal.New(1, int32(decimals))

	decimalQuant := decimal.NewFromBigInt(rawValue, 0)
	amount := math.MustParsePrecFloat64(decimalQuant.Div(pow).InexactFloat64(), data.MaxAmountPrecision)
	if amount <= 0 {
		log.Sugar.Warnf("[%s-%s][%s] skip non-positive amount %.2f", net, tokenSym, walletAddr, amount)
		return
	}
	if tokenConfig.MinAmount > 0 && amount < tokenConfig.MinAmount {
		log.Sugar.Debugf("[%s-%s][%s] skip below min amount hash=%s amount=%.2f min=%.2f", net, tokenSym, walletAddr, txHash, amount, tokenConfig.MinAmount)
		return
	}

	log.Sugar.Debugf("[%s-%s][%s] processing transfer hash=%s amount=%.2f", net, tokenSym, walletAddr, txHash, amount)

	order, fromLock, err := resolveEvmTransferOrder(chainNetwork, walletAddr, tokenSym, amount, blockTsMs)
	if err != nil {
		log.Sugar.Warnf("[%s-%s][%s] load order candidate: %v", net, tokenSym, walletAddr, err)
		return fmt.Errorf("读取待支付订单 network=%s token=%s address=%s: %w", chainNetwork, tokenSym, walletAddr, err)
	}
	if order == nil || order.ID == 0 {
		order, err = data.GetOrderByBlockTransactionIDsCaseInsensitive([]string{txHash})
		if err != nil {
			return fmt.Errorf("按交易哈希读取已提交订单 hash=%s: %w", txHash, err)
		}
		fromLock = false
	}
	if order == nil || order.ID == 0 {
		log.Sugar.Warnf("[%s-%s][%s] skip unmatched tx hash=%s amount=%.2f", net, tokenSym, walletAddr, txHash, amount)
		return
	}
	if strings.ToLower(strings.TrimSpace(order.Network)) != chainNetwork {
		log.Sugar.Warnf("[%s-%s][%s] skip trade_id=%s network=%q", net, tokenSym, walletAddr, order.TradeId, order.Network)
		return fmt.Errorf("订单网络与转账不一致 trade_id=%s order_network=%s transfer_network=%s", order.TradeId, order.Network, chainNetwork)
	}
	if strings.ToUpper(strings.TrimSpace(order.Token)) != tokenSym {
		log.Sugar.Warnf("[%s-%s][%s] skip trade_id=%s token mismatch order=%s", net, tokenSym, walletAddr, order.TradeId, order.Token)
		return fmt.Errorf("订单代币与转账不一致 trade_id=%s order_token=%s transfer_token=%s", order.TradeId, order.Token, tokenSym)
	}
	if !strings.EqualFold(strings.TrimSpace(order.ReceiveAddress), walletAddr) {
		return fmt.Errorf("订单收款地址与转账不一致 trade_id=%s order_address=%s transfer_address=%s", order.TradeId, order.ReceiveAddress, walletAddr)
	}
	precision := int32(data.GetAmountPrecision())
	if !decimal.NewFromFloat(order.ActualAmount).Round(precision).Equal(decimal.NewFromFloat(amount).Round(precision)) {
		return fmt.Errorf("订单金额与转账不一致 trade_id=%s order_amount=%v transfer_amount=%v", order.TradeId, order.ActualAmount, amount)
	}
	if blockTsMs > 0 && blockTsMs < order.CreatedAt.TimestampMilli() {
		log.Sugar.Warnf("[%s-%s][%s] skip tx %s because block time %d is before order create time %d", net, tokenSym, walletAddr, txHash, blockTsMs, order.CreatedAt.TimestampMilli())
		return
	}

	allowedStatuses, expirationTs, payable := evmTransferAllowedStatuses(order, blockTsMs)
	if !payable {
		log.Sugar.Warnf("[%s-%s][%s] skip expired trade_id=%s because block time %d is after expiration %d", net, tokenSym, walletAddr, order.TradeId, blockTsMs, expirationTs)
		return
	}

	if fromLock {
		log.Sugar.Debugf("[%s-%s][%s] lock matched trade_id=%s", net, tokenSym, walletAddr, order.TradeId)
	} else {
		log.Sugar.Infof("[%s-%s][%s] recovered order from orders table trade_id=%s status=%d", net, tokenSym, walletAddr, order.TradeId, order.Status)
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     walletAddr,
		Token:              tokenSym,
		Network:            chainNetwork,
		TradeId:            order.TradeId,
		Amount:             amount,
		BlockTransactionId: txHash,
	}
	err = orderProcessingWithAllowedStatuses(req, allowedStatuses)
	if err != nil {
		if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
			log.Sugar.Infof("[%s-%s][%s] skip resolved trade_id=%s hash=%s err=%v", net, tokenSym, walletAddr, order.TradeId, txHash, err)
			return
		}
		log.Sugar.Errorf("[%s-%s][%s] OrderProcessing: %v", net, tokenSym, walletAddr, err)
		return fmt.Errorf("处理 EVM 支付订单 trade_id=%s hash=%s: %w", order.TradeId, txHash, err)
	}

	sendPaymentNotification(order)
	log.Sugar.Infof("[%s-%s][%s] payment processed trade_id=%s hash=%s", net, tokenSym, walletAddr, order.TradeId, txHash)
	return nil
}

func evmTransferAllowedStatuses(order *mdb.Orders, blockTsMs int64) ([]int, int64, bool) {
	expirationTs := order.CreatedAt.AddMinutes(config.GetOrderExpirationTime()).TimestampMilli()
	if blockTsMs > 0 && blockTsMs <= expirationTs {
		// 支付发生在有效期内时同时允许 WaitPay/Expired，消除过期任务与链监听的状态竞态。
		return []int{mdb.StatusWaitPay, mdb.StatusExpired}, expirationTs, true
	}
	if order.Status == mdb.StatusExpired {
		return nil, expirationTs, false
	}
	return []int{mdb.StatusWaitPay}, expirationTs, true
}

func resolveEvmTransferOrder(chainNetwork string, walletAddr string, tokenSym string, amount float64, blockTsMs int64) (*mdb.Orders, bool, error) {
	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(chainNetwork, walletAddr, tokenSym, amount)
	if err != nil {
		return nil, false, err
	}
	if tradeID != "" {
		order, err := data.GetOrderInfoByTradeId(tradeID)
		if err != nil {
			return nil, false, err
		}
		if order != nil && order.ID > 0 {
			return order, true, nil
		}
	}

	before := time.Now()
	if blockTsMs > 0 {
		before = time.UnixMilli(blockTsMs)
	}
	order, err := data.GetOnChainOrderByWalletAddressAndAmountAndTokenBeforeTime(chainNetwork, walletAddr, tokenSym, amount, before)
	if err != nil {
		return nil, false, err
	}
	if order == nil || order.ID == 0 {
		return nil, false, nil
	}
	return order, false, nil
}

func sendPaymentNotification(order *mdb.Orders) {
	if order == nil {
		return
	}
	if strings.TrimSpace(order.TradeId) != "" {
		latest, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil {
			log.Sugar.Warnf("[notify] reload order failed trade_id=%s err=%v", order.TradeId, err)
		} else if latest != nil && latest.TradeId != "" {
			order = latest
		}
	}

	precision := data.GetAmountPrecision()
	amountFormat := fmt.Sprintf("%%.%df", precision)
	msg := fmt.Sprintf(
		"🎉 <b>收款成功通知</b>\n\n"+
			"💰 <b>金额信息</b>\n"+
			"├ 订单金额：<code>"+amountFormat+" %s</code>\n"+
			"└ 实际到账：<code>"+amountFormat+" %s</code>\n\n"+
			"📋 <b>订单信息</b>\n"+
			"├ 交易号：<code>%s</code>\n"+
			"├ 订单号：<code>%s</code>\n"+
			"├ 网络：<code>%s</code>\n"+
			"└ 钱包地址：<code>%s</code>\n\n"+
			"⏰ <b>时间信息</b>\n"+
			"├ 创建时间：%s\n"+
			"└ 支付时间：%s",
		order.Amount,
		strings.ToUpper(order.Currency),
		order.ActualAmount,
		strings.ToUpper(order.Token),
		order.TradeId,
		order.OrderId,
		networkDisplay(order.Network),
		order.ReceiveAddress,
		order.CreatedAt.ToDateTimeString(),
		order.UpdatedAt.ToDateTimeString(),
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
	case mdb.NetworkBase:
		return "Base"
	case mdb.NetworkArbitrum:
		return "Arbitrum One"
	default:
		if n == "" {
			return "Tron"
		}
		return strings.ToUpper(n)
	}
}
