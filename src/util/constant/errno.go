package constant

import "errors"

var Errno = map[int]string{
	400:   "system error",
	401:   "signature verification failed",
	10001: "wallet address already exists",
	10002: "order already exists",
	10003: "no available wallet address",
	10004: "invalid payment amount",
	10005: "no available amount channel",
	10006: "rate calculation failed",
	10007: "block transaction already processed",
	10008: "order does not exist",
	10009: "failed to parse request params",
	10010: "order status already changed",
	10011: "exceeded maximum sub-order limit",
	10012: "cannot switch network on a sub-order",
	10013: "order is not awaiting payment",
	10014: "chain is not enabled",
	10016: "supported asset not found",
	10017: "payment provider is not enabled",
	10018: "payment provider configuration is incomplete",
	10019: "payment provider does not support this token or network",
	10020: "invalid rpc node purpose",
	10021: "invalid rpc node url",
	10022: "rpc node http url required",
	10023: "rpc node websocket url required",
	10024: "invalid rpc node type",
	10025: "invalid username or password",
	10026: "admin user disabled",
	10027: "admin unauthorized",
	10028: "admin user not found",
	10029: "old password incorrect",
	10030: "wallet not found",
	10031: "api key not found",
	10032: "rpc node not found",
	10033: "order callback not applicable",
	10034: "order notify url empty",
	10035: "resend callback failed",
	10036: "invalid notification config",
	10037: "invalid notification events",
	10038: "manual payment verification failed",
	10039: "manual payment only supports on-chain orders",
	10040: "initial admin password unavailable, check from logs",
	10041: "invalid notify url",
	10042: "payment provider order creation failed",
	10043: "invalid setting item",
	10044: "invalid order redirect url",
	10045: "order api key unavailable",
	10046: "failed to build epay return signature",
}

var (
	SystemErr                  = Err(400)
	SignatureErr               = Err(401)
	WalletAddressAlreadyExists = Err(10001)
	OrderAlreadyExists         = Err(10002)
	NotAvailableWalletAddress  = Err(10003)
	PayAmountErr               = Err(10004)
	NotAvailableAmountErr      = Err(10005)
	RateAmountErr              = Err(10006)
	OrderBlockAlreadyProcess   = Err(10007)
	OrderNotExists             = Err(10008)
	ParamsMarshalErr           = Err(10009)
	OrderStatusConflict        = Err(10010)
	SubOrderLimitExceeded      = Err(10011)
	CannotSwitchSubOrder       = Err(10012)
	OrderNotWaitPay            = Err(10013)
	ChainNotEnabled            = Err(10014)
	SupportedAssetNotFound     = Err(10016)
	PaymentProviderNotEnabled  = Err(10017)
	PaymentProviderConfigErr   = Err(10018)
	PaymentProviderNotSupport  = Err(10019)
	RpcNodePurposeErr          = Err(10020)
	RpcNodeURLErr              = Err(10021)
	RpcNodeHTTPURLErr          = Err(10022)
	RpcNodeWebSocketURLErr     = Err(10023)
	RpcNodeTypeErr             = Err(10024)
	AdminInvalidCredentialErr  = Err(10025)
	AdminUserDisabledErr       = Err(10026)
	AdminUnauthorizedErr       = Err(10027)
	AdminUserNotFoundErr       = Err(10028)
	AdminOldPasswordErr        = Err(10029)
	WalletNotFoundErr          = Err(10030)
	ApiKeyNotFoundErr          = Err(10031)
	RpcNodeNotFoundErr         = Err(10032)
	OrderCallbackNotApplicable = Err(10033)
	OrderNotifyURLEmptyErr     = Err(10034)
	OrderResendCallbackErr     = Err(10035)
	NotificationConfigErr      = Err(10036)
	NotificationEventsErr      = Err(10037)
	ManualPaymentVerifyErr     = Err(10038)
	ManualPaymentProviderErr   = Err(10039)
	InitialAdminPasswordErr    = Err(10040)
	NotifyURLErr               = Err(10041)
	PaymentProviderCreateErr   = Err(10042)
	SettingItemErr             = Err(10043)
	OrderRedirectURLErr        = Err(10044)
	OrderApiKeyUnavailableErr  = Err(10045)
	EPayReturnSignatureErr     = Err(10046)
)

type RspError struct {
	Code int
	Msg  string
}

func (re *RspError) Error() string {
	return re.Msg
}

func Err(code int) (err error) {
	err = &RspError{
		Code: code,
		Msg:  Errno[code],
	}
	return err
}

func (re *RspError) Render() (code int, msg string) {
	return re.Code, re.Msg
}

func ResolveErrno(err error) (code int, msg string) {
	var rspErr *RspError
	if errors.As(err, &rspErr) {
		return rspErr.Render()
	}
	return SystemErr.(*RspError).Render()
}

// HttpStatus maps a RspError code to the HTTP status the handler
// should use on the wire. Small codes (< 1000) are treated as real
// HTTP status codes (e.g. 400 system error, 401 signature failure) so
// clients see the right status. Business codes (>= 1000) are all
// client-side problems that map to HTTP 400; the specific code still
// lives in the body's `status_code` field for the frontend to branch on.
func (re *RspError) HttpStatus() int {
	if re == nil {
		return 500
	}
	if re.Code >= 400 && re.Code < 600 {
		return re.Code
	}
	return 400
}
