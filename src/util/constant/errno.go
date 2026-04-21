package constant

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
	10014: "supported asset already exists",
	10015: "supported asset not found",
	10016: "invalid wallet address",
	10017: "payment method unavailable",
}

var (
	SystemErr                   = Err(400)
	SignatureErr                = Err(401)
	WalletAddressAlreadyExists  = Err(10001)
	OrderAlreadyExists          = Err(10002)
	NotAvailableWalletAddress   = Err(10003)
	PayAmountErr                = Err(10004)
	NotAvailableAmountErr       = Err(10005)
	RateAmountErr               = Err(10006)
	OrderBlockAlreadyProcess    = Err(10007)
	OrderNotExists              = Err(10008)
	ParamsMarshalErr            = Err(10009)
	OrderStatusConflict         = Err(10010)
	SubOrderLimitExceeded       = Err(10011)
	CannotSwitchSubOrder        = Err(10012)
	OrderNotWaitPay             = Err(10013)
	SupportedAssetAlreadyExists = Err(10014)
	SupportedAssetNotFound      = Err(10015)
	InvalidWalletAddress        = Err(10016)
	PaymentMethodUnavailable    = Err(10017)
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
