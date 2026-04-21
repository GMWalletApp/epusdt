package service

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/assimon/luuu/config"
	tron "github.com/assimon/luuu/crypto"
	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/model/request"
	"github.com/assimon/luuu/telegram"
	"github.com/assimon/luuu/util/constant"
	"github.com/assimon/luuu/util/http_client"
	"github.com/assimon/luuu/util/log"
	"github.com/assimon/luuu/util/math"
	"github.com/dromara/carbon/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gookit/goutil/stdutil"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

const TRC20_USDT_ID = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
const tronGridPageLimit = 100

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

func cloneQueryParams(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src)+1)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func walkTronGridPages(fetchPage func(map[string]string) ([]byte, error), baseQuery map[string]string, visit func(gjson.Result) bool) error {
	fingerprint := ""
	for {
		query := cloneQueryParams(baseQuery)
		if fingerprint != "" {
			query["fingerprint"] = fingerprint
		}
		body, err := fetchPage(query)
		if err != nil {
			return err
		}
		records := gjson.GetBytes(body, "data").Array()
		if len(records) == 0 {
			return nil
		}
		for _, record := range records {
			if !visit(record) {
				return nil
			}
		}
		next := gjson.GetBytes(body, "meta.fingerprint").String()
		if next == "" {
			return nil
		}
		fingerprint = next
	}
}

func checkTrxTransfers(address string, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[TRX][%s] panic recovered: %v", address, err)
		}
	}()

	client := http_client.GetHttpClient()
	startTime := time.Now().Add(-config.GetOrderExpirationTimeDuration() - 5*time.Minute).UnixMilli()
	endTime := carbon.Now().TimestampMilli()
	url := fmt.Sprintf("https://api.trongrid.io/v1/accounts/%s/transactions", address)
	err := walkTronGridPages(func(query map[string]string) ([]byte, error) {
		resp, err := client.R().SetQueryParams(query).SetHeader("TRON-PRO-API-KEY", config.TRON_GRID_API_KEY).Get(url)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode() != http.StatusOK {
			return nil, fmt.Errorf("TRX API returned status %d", resp.StatusCode())
		}
		if !gjson.GetBytes(resp.Body(), "success").Bool() {
			return nil, errors.New("TRX API response indicates failure")
		}
		return resp.Body(), nil
	}, map[string]string{
		"order_by":      "block_timestamp,desc",
		"limit":         stdutil.ToString(tronGridPageLimit),
		"only_to":       "true",
		"min_timestamp": stdutil.ToString(startTime),
		"max_timestamp": stdutil.ToString(endTime),
	}, func(transfer gjson.Result) bool {
		blockTimestamp := transfer.Get("block_timestamp").Int()
		if blockTimestamp < startTime {
			return false
		}
		if transfer.Get("raw_data.contract.0.type").String() != "TransferContract" {
			return true
		}
		if transfer.Get("ret.0.contractRet").String() != "SUCCESS" {
			return true
		}

		toAddressHex := transfer.Get("raw_data.contract.0.parameter.value.to_address").String()
		toBytes, err := hex.DecodeString(toAddressHex)
		if err != nil {
			log.Sugar.Errorf("[TRX][%s] decode address failed: %v", address, err)
			return true
		}
		if tron.EncodeCheck(toBytes) != address {
			return true
		}

		rawAmount := transfer.Get("raw_data.contract.0.parameter.value.amount").String()
		decimalQuant, err := decimal.NewFromString(rawAmount)
		if err != nil {
			log.Sugar.Errorf("[TRX][%s] parse amount failed: %v", address, err)
			return true
		}
		amount := math.MustParsePrecFloat64(decimalQuant.Div(decimal.NewFromInt(1000000)).InexactFloat64(), 2)
		if amount <= 0 {
			return true
		}

		txID := transfer.Get("txID").String()
		tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, address, "TRX", amount)
		if err != nil {
			panic(err)
		}
		if tradeID == "" {
			log.Sugar.Debugf("[TRX][%s] skip unmatched tx hash=%s amount=%.2f", address, txID, amount)
			return true
		}
		log.Sugar.Infof("[TRX][%s] matched trade_id=%s hash=%s amount=%.2f", address, tradeID, txID, amount)

		order, err := data.GetOrderInfoByTradeId(tradeID)
		if err != nil {
			panic(err)
		}
		createTime := order.CreatedAt.TimestampMilli()
		if blockTimestamp < createTime {
			log.Sugar.Warnf("[TRX][%s] skip tx %s because block time %d is before order create time %d", address, txID, blockTimestamp, createTime)
			return true
		}

		req := &request.OrderProcessingRequest{
			ReceiveAddress:     address,
			Token:              "TRX",
			Network:            mdb.NetworkTron,
			TradeId:            tradeID,
			Amount:             amount,
			BlockTransactionId: txID,
		}
		err = OrderProcessing(req)
		if err != nil {
			if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
				log.Sugar.Infof("[TRX][%s] skip resolved transfer trade_id=%s hash=%s err=%v", address, tradeID, txID, err)
				return true
			}
			panic(err)
		}

		sendPaymentNotification(order)
		log.Sugar.Infof("[TRX][%s] payment processed trade_id=%s hash=%s", address, tradeID, txID)
		return true
	})
	if err != nil {
		panic(err)
	}
}

func checkTrc20Transfers(address string, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[TRC20][%s] panic recovered: %v", address, err)
		}
	}()

	client := http_client.GetHttpClient()
	startTime := time.Now().Add(-config.GetOrderExpirationTimeDuration() - 5*time.Minute).UnixMilli()
	endTime := carbon.Now().TimestampMilli()
	url := fmt.Sprintf("https://api.trongrid.io/v1/accounts/%s/transactions/trc20", address)
	err := walkTronGridPages(func(query map[string]string) ([]byte, error) {
		resp, err := client.R().SetQueryParams(query).SetHeader("TRON-PRO-API-KEY", config.TRON_GRID_API_KEY).Get(url)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode() != http.StatusOK {
			return nil, fmt.Errorf("TRC20 API returned status %d", resp.StatusCode())
		}
		if !gjson.GetBytes(resp.Body(), "success").Bool() {
			return nil, errors.New("TRC20 API response indicates failure")
		}
		return resp.Body(), nil
	}, map[string]string{
		"order_by":      "block_timestamp,desc",
		"limit":         stdutil.ToString(tronGridPageLimit),
		"only_to":       "true",
		"min_timestamp": stdutil.ToString(startTime),
		"max_timestamp": stdutil.ToString(endTime),
	}, func(transfer gjson.Result) bool {
		blockTimestamp := transfer.Get("block_timestamp").Int()
		if blockTimestamp < startTime {
			return false
		}
		if transfer.Get("token_info.address").String() != TRC20_USDT_ID {
			return true
		}
		if transfer.Get("to").String() != address {
			return true
		}

		valueStr := transfer.Get("value").String()
		decimalQuant, err := decimal.NewFromString(valueStr)
		if err != nil {
			log.Sugar.Errorf("[TRC20][%s] parse value failed: %v", address, err)
			return true
		}
		tokenDecimals := transfer.Get("token_info.decimals").Int()
		amount := math.MustParsePrecFloat64(decimalQuant.Div(decimal.New(1, int32(tokenDecimals))).InexactFloat64(), 2)
		if amount <= 0 {
			return true
		}

		txID := transfer.Get("transaction_id").String()
		tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, address, "USDT", amount)
		if err != nil {
			panic(err)
		}
		if tradeID == "" {
			log.Sugar.Debugf("[TRC20][%s] skip unmatched tx hash=%s amount=%.2f", address, txID, amount)
			return true
		}
		log.Sugar.Infof("[TRC20][%s] matched trade_id=%s hash=%s amount=%.2f", address, tradeID, txID, amount)

		order, err := data.GetOrderInfoByTradeId(tradeID)
		if err != nil {
			panic(err)
		}
		createTime := order.CreatedAt.TimestampMilli()
		if blockTimestamp < createTime {
			log.Sugar.Warnf("[TRC20][%s] skip tx %s because block time %d is before order create time %d", address, txID, blockTimestamp, createTime)
			return true
		}

		req := &request.OrderProcessingRequest{
			ReceiveAddress:     address,
			Token:              "USDT",
			Network:            mdb.NetworkTron,
			TradeId:            tradeID,
			Amount:             amount,
			BlockTransactionId: txID,
		}
		err = OrderProcessing(req)
		if err != nil {
			if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
				log.Sugar.Infof("[TRC20][%s] skip resolved transfer trade_id=%s hash=%s err=%v", address, tradeID, txID, err)
				return true
			}
			panic(err)
		}

		sendPaymentNotification(order)
		log.Sugar.Infof("[TRC20][%s] payment processed trade_id=%s hash=%s", address, tradeID, txID)
		return true
	})
	if err != nil {
		panic(err)
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

// TryProcessEvmERC20Transfer 处理各 EVM 链上 USDT/USDC（及 Polygon USDC.e）的 Transfer 入账（合约与 network 需一致）。
func TryProcessEvmERC20Transfer(chainNetwork string, contract common.Address, toAddr common.Address, rawValue *big.Int, txHash string, blockTsMs int64) {
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[%s-WS] TryProcessEvmERC20Transfer panic: %v", evmChainLogLabel(chainNetwork), err)
		}
	}()

	var usdt, usdc common.Address
	var polygonUsdcE common.Address
	switch chainNetwork {
	case mdb.NetworkEthereum:
		usdt = common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")
		usdc = common.HexToAddress("0xA0b86991c6218b36c1d19d4a2e9eb0ce3606eb48")
	case mdb.NetworkBsc:
		usdt = common.HexToAddress("0x55d398326f99059fF775485246999027B3197955")
		usdc = common.HexToAddress("0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d")
	case mdb.NetworkPolygon:
		usdt = common.HexToAddress("0xc2132D05D31c914a87C6611C10748AEb04B58e8F")
		usdc = common.HexToAddress("0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359")
		polygonUsdcE = common.HexToAddress("0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174")
	case mdb.NetworkPlasma:
		// USDT0（官方），6 decimals；链上暂无与 ETH 同级的 Circle USDC 部署，仅匹配 USDT 订单
		usdt = common.HexToAddress("0xB8CE59FC3717ada4C02eaDF9682A9e934F625ebb")
	default:
		return
	}

	var tokenSym string
	switch {
	case contract == usdt:
		tokenSym = "USDT"
	case contract == usdc || (polygonUsdcE != (common.Address{}) && contract == polygonUsdcE):
		tokenSym = "USDC"
	default:
		net := evmChainLogLabel(chainNetwork)
		log.Sugar.Warnf("[%s-WS] skip unsupported contract %s", net, contract.Hex())
		return
	}

	net := evmChainLogLabel(chainNetwork)
	walletAddr := strings.ToLower(toAddr.Hex())
	if rawValue == nil || rawValue.Sign() <= 0 {
		log.Sugar.Infof("[%s-%s][%s] skip non-positive or nil amount", net, tokenSym, walletAddr)
		return
	}
	decimalQuant := decimal.NewFromBigInt(rawValue, 0)
	amount := math.MustParsePrecFloat64(decimalQuant.Div(decimal.NewFromInt(1_000_000)).InexactFloat64(), 2)
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
	if blockTsMs > 0 && blockTsMs < order.CreatedAt.TimestampMilli() {
		log.Sugar.Warnf("[%s-%s][%s] skip tx hash=%s because block time %d is before order create time %d",
			net, tokenSym, walletAddr, txHash, blockTsMs, order.CreatedAt.TimestampMilli())
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
		order.Amount,
		strings.ToUpper(order.Currency),
		order.ActualAmount,
		strings.ToUpper(order.Token),
		order.TradeId,
		order.OrderId,
		networkDisplay(order.Network),
		order.ReceiveAddress,
		order.CreatedAt.ToDateTimeString(),
		carbon.Now().ToDateTimeString(),
	)
	telegram.SendToBot(msg)
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
