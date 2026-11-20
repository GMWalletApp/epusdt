package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/notify"
	"github.com/GMWalletApp/epusdt/util/log"
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
	default:
		if n == "" {
			return "Tron"
		}
		return strings.ToUpper(n)
	}
}
