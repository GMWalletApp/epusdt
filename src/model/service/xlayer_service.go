package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/http_client"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/math"
)

// X Layer hosted checkout via OKX Onchain OS Payment.
//
// Docs: ~/workspace/user/files/onchainos-payment-api-20261116.pdf
// (Onchain OS Payment 商家支付 API 参考, 2026-11-16).
//
// Flow (no webhook exists — polling only):
//  1. POST /api/v6/pay/onchainos-payment/create with the merchant order id,
//     the merchant receiving address (to), a fiat reference amount and one
//     quote (chainId "196", token contract, tokenAmount in smallest units).
//     The response carries paymentIntentId + checkoutUrl.
//  2. The buyer is redirected to checkoutUrl and signs an EIP-3009
//     authorization in their own wallet; the platform submits the transfer
//     and pays the gas. Funds settle directly into `to`.
//  3. The merchant polls POST /query until status=settled. Only settled
//     confirms payment success — page redirects and txHash alone do not.
//
// Auth on every call follows the OKX API-Key spec: headers OK-ACCESS-KEY,
// OK-ACCESS-SIGN, OK-ACCESS-PASSPHRASE, OK-ACCESS-TIMESTAMP, where
// OK-ACCESS-SIGN = Base64(HMAC-SHA256(secret,
// timestamp + method + requestPath + body)) and timestamp is ISO 8601 UTC
// with milliseconds.

const (
	// xlayerAPIBaseURL is the fixed Onchain OS Payment API base URL.
	// It is deliberately NOT configurable: the integration only talks to
	// the official OKX endpoint, and the legacy xlayer.api_url settings
	// key is no longer read (2026-11-21 4th review).
	xlayerAPIBaseURL    = "https://web3.okx.com"
	xlayerAPIPathCreate = "/api/v6/pay/onchainos-payment/create"
	xlayerAPIPathQuery  = "/api/v6/pay/onchainos-payment/query"
	xlayerChainID       = "196"
	xlayerTokenDecimals = 6
)

// xlayerTokenContracts maps normalized token symbols to their X Layer
// contract addresses. The doc writes the second token as USD₮0; it is
// normalized to the ASCII symbol USDT0 everywhere in this codebase.
var xlayerTokenContracts = map[string]string{
	"USDC":  "0xb6ceceab302e2e4948951ee7843fc24e92933061",
	"USDT0": "0x779ded0c9e1022225f8e0630b35a9b54be713736",
	"USDG":  "0x4ae46a509f6b1d9056937ba4500cb143933d2dc8",
}

// normalizeXLayerToken upper-cases a token symbol and folds the doc's USD₮0
// spelling into the ASCII USDT0 used by the cashier and the settings.
func normalizeXLayerToken(token string) string {
	normalized := strings.ToUpper(strings.TrimSpace(token))
	normalized = strings.ReplaceAll(normalized, "₮", "T")
	return normalized
}

// xlayerTokenAllowed reports whether the token is in the admin allow-list.
func xlayerTokenAllowed(token string) bool {
	normalized := normalizeXLayerToken(token)
	for _, item := range data.GetXLayerAllowTokens() {
		if normalizeXLayerToken(item) == normalized {
			return true
		}
	}
	return false
}

// xlayerTokenContract returns the X Layer contract address for a token.
func xlayerTokenContract(token string) (string, bool) {
	contract, ok := xlayerTokenContracts[normalizeXLayerToken(token)]
	return contract, ok
}

// xlayerSign builds the OK-ACCESS-SIGN header value.
func xlayerSign(secret, timestamp, method, requestPath, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + method + requestPath + body))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func xlayerTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

type xlayerPaymentToken struct {
	ChainID      string `json:"chainId"`
	TokenAddress string `json:"tokenAddress"`
	TokenAmount  string `json:"tokenAmount"`
}

type xlayerQuote struct {
	PaymentToken xlayerPaymentToken `json:"paymentToken"`
}

type xlayerCreateRequest struct {
	MerchantPaymentID   string        `json:"merchantPaymentId"`
	To                  string        `json:"to"`
	FiatAmount          string        `json:"fiatAmount"`
	FiatCurrency        string        `json:"fiatCurrency"`
	Quotes              []xlayerQuote `json:"quotes"`
	ReturnURL           string        `json:"returnUrl,omitempty"`
	MerchantDisplayName string        `json:"merchantDisplayName,omitempty"`
	ProductDesc         string        `json:"productDesc,omitempty"`
	ExpireSeconds       int           `json:"expireSeconds,omitempty"`
}

type xlayerCreateData struct {
	MerchantPaymentID string `json:"merchantPaymentId"`
	PaymentIntentID   string `json:"paymentIntentId"`
	Status            string `json:"status"`
	To                string `json:"to"`
	CheckoutURL       string `json:"checkoutUrl"`
	ExpiresAt         string `json:"expiresAt"`
}

type xlayerCreateResponse struct {
	Code int               `json:"code"`
	Msg  string            `json:"msg"`
	Data *xlayerCreateData `json:"data"`
}

type xlayerQueryPaymentToken struct {
	ChainID      string `json:"chainId"`
	TokenSymbol  string `json:"tokenSymbol"`
	TokenAddress string `json:"tokenAddress"`
	TokenDecimal int    `json:"tokenDecimal"`
	TokenAmount  string `json:"tokenAmount"`
}

type xlayerQueryData struct {
	MerchantPaymentID string                   `json:"merchantPaymentId"`
	PaymentIntentID   string                   `json:"paymentIntentId"`
	Status            string                   `json:"status"`
	To                string                   `json:"to"`
	TxHash            string                   `json:"txHash"`
	FiatAmount        string                   `json:"fiatAmount"`
	FiatCurrency      string                   `json:"fiatCurrency"`
	PaymentToken      *xlayerQueryPaymentToken `json:"paymentToken"`
	ExpiresAt         string                   `json:"expiresAt"`
}

type xlayerQueryResponse struct {
	Code int              `json:"code"`
	Msg  string           `json:"msg"`
	Data *xlayerQueryData `json:"data"`
}

type xlayerCreateOrderResult struct {
	PaymentIntentID string
	CheckoutURL     string
	ExpiresAt       string
	Status          string
}

// xlayerPost performs one signed POST against the Onchain OS Payment API and
// decodes the shared {code, msg, data} envelope. The request body is
// marshalled exactly once and the same bytes are signed and sent, because
// the provider verifies the signature over the received body.
func xlayerPost(requestPath string, body []byte, result interface{}) (string, error) {
	apiKey := data.GetXLayerAPIKey()
	secret := data.GetXLayerAPISecret()
	passphrase := data.GetXLayerPassphrase()
	if apiKey == "" || secret == "" || passphrase == "" {
		return "", fmt.Errorf("xlayer config incomplete")
	}

	bodyStr := string(body)
	timestamp := xlayerTimestamp()
	sign := xlayerSign(secret, timestamp, "POST", requestPath, bodyStr)

	client := http_client.GetHttpClient()
	client.SetTimeout(time.Duration(data.GetXLayerTimeoutSeconds()) * time.Second)

	resp, err := client.R().
		SetHeaders(map[string]string{
			"Content-Type":         "application/json",
			"OK-ACCESS-KEY":        apiKey,
			"OK-ACCESS-SIGN":       sign,
			"OK-ACCESS-PASSPHRASE": passphrase,
			"OK-ACCESS-TIMESTAMP":  timestamp,
		}).
		SetBody(bodyStr).
		SetResult(result).
		Post(xlayerAPIBaseURL + requestPath)
	if err != nil {
		log.Sugar.Warnf("[xlayer] request failed path=%s err=%v", requestPath, err)
		return "", err
	}
	if resp.StatusCode() >= 400 {
		log.Sugar.Warnf("[xlayer] http error path=%s status=%s body=%s", requestPath, resp.Status(), resp.String())
		return resp.String(), fmt.Errorf("xlayer http status: %s", resp.Status())
	}
	return resp.String(), nil
}

// createXLayerOrder creates a hosted checkout order and returns the
// paymentIntentId + checkoutUrl the buyer must be redirected to.
// merchantPaymentID must be unique per API key; reusing one returns 61009.
func createXLayerOrder(merchantPaymentID, to, fiatAmount, fiatCurrency, token, tokenAmountBaseUnits, returnURL, merchantDisplayName, productDesc string, expireSeconds int) (*xlayerCreateOrderResult, error) {
	contract, ok := xlayerTokenContract(token)
	if !ok {
		return nil, fmt.Errorf("xlayer unsupported token: %s", token)
	}
	req := xlayerCreateRequest{
		MerchantPaymentID: merchantPaymentID,
		To:                to,
		FiatAmount:        fiatAmount,
		FiatCurrency:      fiatCurrency,
		Quotes: []xlayerQuote{{
			PaymentToken: xlayerPaymentToken{
				ChainID:      xlayerChainID,
				TokenAddress: contract,
				TokenAmount:  tokenAmountBaseUnits,
			},
		}},
		ReturnURL:           returnURL,
		MerchantDisplayName: merchantDisplayName,
		ProductDesc:         productDesc,
		ExpireSeconds:       expireSeconds,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	var payload xlayerCreateResponse
	if _, err := xlayerPost(xlayerAPIPathCreate, body, &payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 {
		log.Sugar.Warnf("[xlayer] create rejected merchant_payment_id=%s code=%d msg=%s", merchantPaymentID, payload.Code, payload.Msg)
		return nil, fmt.Errorf("xlayer create failed code=%d msg=%s", payload.Code, payload.Msg)
	}
	if payload.Data == nil || payload.Data.PaymentIntentID == "" || payload.Data.CheckoutURL == "" {
		log.Sugar.Warnf("[xlayer] create response missing checkout merchant_payment_id=%s code=%d msg=%s", merchantPaymentID, payload.Code, payload.Msg)
		return nil, fmt.Errorf("xlayer create response missing paymentIntentId/checkoutUrl")
	}
	return &xlayerCreateOrderResult{
		PaymentIntentID: payload.Data.PaymentIntentID,
		CheckoutURL:     payload.Data.CheckoutURL,
		ExpiresAt:       payload.Data.ExpiresAt,
		Status:          payload.Data.Status,
	}, nil
}

// queryXLayerOrder fetches the current provider-side status of one order by
// its paymentIntentId. It also returns the raw response body for auditing.
func queryXLayerOrder(paymentIntentID string) (*xlayerQueryData, string, error) {
	body, err := json.Marshal(map[string]string{"paymentIntentId": paymentIntentID})
	if err != nil {
		return nil, "", err
	}
	var payload xlayerQueryResponse
	raw, err := xlayerPost(xlayerAPIPathQuery, body, &payload)
	if err != nil {
		return nil, raw, err
	}
	if payload.Code != 0 {
		return nil, raw, fmt.Errorf("xlayer query failed code=%d msg=%s", payload.Code, payload.Msg)
	}
	if payload.Data == nil {
		return nil, raw, fmt.Errorf("xlayer query response missing data")
	}
	return payload.Data, raw, nil
}

// xlayerAmounts converts a fiat order amount into the token amount the
// provider charges. All three X Layer tokens are USD stablecoins priced 1:1,
// so the USDC rate is used for every token (the forced-rate list only
// carries usdt/usdc). It returns the display amount (amount-precision
// rounded, as used by the order row), the exact token amount, and the
// smallest-unit amount string sent to the provider. The provider API
// enforces no minimum amount (confirmed by the user 2026-11-18), so no
// local minimum is applied here; a too-small amount fails at the provider
// instead, if it enforces one.
func xlayerAmounts(fiatAmount float64, currency string) (displayAmount float64, tokenAmount decimal.Decimal, tokenAmountBaseUnits string, err error) {
	rate := config.GetRateForCoin("usdc", strings.ToLower(strings.TrimSpace(currency)))
	if rate <= 0 {
		return 0, decimal.Decimal{}, "", constant.RateAmountErr
	}
	tokenAmount = decimal.NewFromFloat(fiatAmount).Mul(decimal.NewFromFloat(rate))
	baseUnits := tokenAmount.Mul(decimal.NewFromInt(1_000_000)).Truncate(0)
	displayAmount = math.MustParsePrecFloat64(tokenAmount.InexactFloat64(), data.GetAmountPrecision())
	return displayAmount, tokenAmount, baseUnits.String(), nil
}

// xlayerExpireSeconds bounds the provider-side order lifetime by the local
// order expiration so the checkout never outlives the epusdt order.
func xlayerExpireSeconds() int {
	seconds := config.GetOrderExpirationTime() * 60
	if seconds <= 0 {
		seconds = 1800
	}
	if seconds > 1800 {
		seconds = 1800
	}
	return seconds
}

// PollXLayerProviderOrders queries the provider for every pending X Layer
// provider order and settles the ones that reached status=settled. The
// provider offers no webhook, so this is the only confirmation path; a
// page redirect or a txHash alone never marks an order paid.
func PollXLayerProviderOrders() {
	rows, err := data.ListPendingProviderOrdersByProvider(mdb.PaymentProviderXLayer)
	if err != nil {
		log.Sugar.Warnf("[xlayer] list pending provider orders failed err=%v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	for _, row := range rows {
		toAddress := row.ReceiveAddress
		if strings.TrimSpace(row.ProviderOrderID) == "" {
			continue
		}
		order, err := data.GetOrderInfoByTradeId(row.TradeId)
		if err != nil {
			log.Sugar.Warnf("[xlayer] load order failed trade_id=%s err=%v", row.TradeId, err)
			continue
		}
		if order.ID <= 0 {
			continue
		}
		result, raw, err := queryXLayerOrder(row.ProviderOrderID)
		if err != nil {
			log.Sugar.Warnf("[xlayer] query failed trade_id=%s payment_intent_id=%s err=%v", row.TradeId, row.ProviderOrderID, err)
			continue
		}
		switch strings.ToLower(strings.TrimSpace(result.Status)) {
		case "settled":
			if !xlayerSettleMatches(order, result, toAddress) {
				continue
			}
			blockTxID := strings.TrimSpace(result.TxHash)
			if blockTxID == "" {
				blockTxID = row.ProviderOrderID
			}
			// The provider state must be reconciled even when the local order
			// already expired before polling observed the settlement. In that
			// case the local order remains expired, while the provider row is
			// still moved to its terminal paid state for reconciliation.
			if order.Status == mdb.StatusWaitPay {
				err = OrderProcessing(&request.OrderProcessingRequest{
					ReceiveAddress:     toAddress,
					Currency:           order.Currency,
					Token:              order.Token,
					Network:            mdb.PaymentProviderXLayer,
					Amount:             order.ActualAmount,
					TradeId:            order.TradeId,
					BlockTransactionId: blockTxID,
				})
				if err != nil && err != constant.OrderBlockAlreadyProcess && err != constant.OrderStatusConflict {
					log.Sugar.Warnf("[xlayer] settle order failed trade_id=%s err=%v", row.TradeId, err)
					continue
				}
				if err == constant.OrderStatusConflict {
					log.Sugar.Warnf("[xlayer] settled payment conflicted with local order state trade_id=%s payment_intent_id=%s tx_hash=%s", row.TradeId, row.ProviderOrderID, blockTxID)
				}
				if err == nil {
					sendPaymentNotification(order)
				}
			} else if order.Status != mdb.StatusPaySuccess {
				log.Sugar.Warnf("[xlayer] provider settled after local order status=%d trade_id=%s payment_intent_id=%s tx_hash=%s", order.Status, row.TradeId, row.ProviderOrderID, blockTxID)
			}
			if err := data.MarkProviderOrderPaid(row.TradeId, mdb.PaymentProviderXLayer, raw); err != nil {
				log.Sugar.Warnf("[xlayer] mark provider order paid failed trade_id=%s err=%v", row.TradeId, err)
			}
		case "expired", "failed":
			log.Sugar.Infof("[xlayer] provider order terminal trade_id=%s status=%s", row.TradeId, result.Status)
			if strings.ToLower(strings.TrimSpace(result.Status)) == "failed" {
				_ = data.MarkProviderOrderFailed(row.TradeId, mdb.PaymentProviderXLayer)
			} else {
				_ = data.MarkProviderOrderExpired(row.TradeId, mdb.PaymentProviderXLayer)
			}
			_ = data.ExpireOrderByTradeID(row.TradeId)
		default:
			// pending / paid: still in flight, keep polling.
		}
	}
}

// xlayerSettleMatches verifies a settled provider result actually belongs
// to this order before any money is acknowledged: same receiving address,
// same token, and a non-empty payment token. The provider guarantees the
// settled amount is the full order amount, so no local amount replay is
// attempted here (the local rate can move between create and settle).
func xlayerSettleMatches(order *mdb.Orders, result *xlayerQueryData, toAddress string) bool {
	if toAddress == "" || !strings.EqualFold(strings.TrimSpace(result.To), strings.TrimSpace(toAddress)) {
		log.Sugar.Warnf("[xlayer] settle to-address mismatch trade_id=%s got=%s want=%s", order.TradeId, result.To, toAddress)
		return false
	}
	if result.PaymentToken == nil {
		log.Sugar.Warnf("[xlayer] settle missing payment token trade_id=%s", order.TradeId)
		return false
	}
	if !strings.EqualFold(normalizeXLayerToken(result.PaymentToken.TokenSymbol), normalizeXLayerToken(order.Token)) {
		log.Sugar.Warnf("[xlayer] settle token mismatch trade_id=%s got=%s want=%s", order.TradeId, result.PaymentToken.TokenSymbol, order.Token)
		return false
	}
	return true
}
