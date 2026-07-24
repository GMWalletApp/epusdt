package telegram

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/lang"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/gookit/goutil/mathutil"
	"github.com/gookit/goutil/strutil"
	tb "gopkg.in/telebot.v3"
)

const (
	pendingWalletAddressTTL = 5 * time.Minute
)

type pendingWalletAddressState struct {
	RequestedAt time.Time
	Network     string
}

var pendingWalletAddressUsers sync.Map

func OnTextMessageHandle(c tb.Context) error {
	msg := c.Message()
	if msg == nil {
		return nil
	}

	sender := c.Sender()
	senderID := int64(0)
	if sender != nil {
		senderID = sender.ID
	}

	isReplyFlow := msg.ReplyTo != nil && strings.HasPrefix(msg.ReplyTo.Text, "请发送 ")
	isPendingFlow := isWalletAddressPending(senderID)
	if !isReplyFlow && !isPendingFlow {
		return nil
	}

	if isReplyFlow {
		defer bots.Delete(msg.ReplyTo)
	}

	msgText := strings.TrimSpace(msg.Text)
	state, _ := getPendingWalletAddressState(senderID)
	if state.Network == "" {
		_ = c.Send(lang.T("select_network_first"))
		return nil
	}

	var err error
	if !isValidAddressByNetwork(state.Network, msgText) {
		_ = c.Send(lang.TF("wallet_add_fail", msgText, strings.ToUpper(state.Network)))
		return nil
	}
	storeAddress := normalizeWalletAddressByNetwork(state.Network, msgText)
	_, err = data.AddWalletAddressWithNetwork(state.Network, storeAddress)
	if err != nil {
		return c.Send(err.Error())
	}
	pendingWalletAddressUsers.Delete(senderID)

	_ = c.Send(lang.TF("wallet_add_success", storeAddress, strings.ToUpper(state.Network)))
	return WalletList(c)
}

func WalletList(c tb.Context) error {
	wallets, err := data.GetAllWalletAddress()
	if err != nil {
		return err
	}

	var btnList [][]tb.InlineButton
	for _, wallet := range wallets {
		status := lang.T("status_enabled")
		if wallet.Status == mdb.TokenStatusDisable {
			status = lang.T("status_disabled")
		}
		net := wallet.Network
		if net == "" {
			net = mdb.NetworkTron
		}

		btnInfo := tb.InlineButton{
			Unique: wallet.Address,
			Text:   fmt.Sprintf("[%s] %s [%s]", net, wallet.Address, status),
			Data:   strutil.MustString(wallet.ID),
		}
		bots.Handle(&btnInfo, WalletInfo)
		btnList = append(btnList, []tb.InlineButton{btnInfo})
	}

	addBtn := tb.InlineButton{Text: lang.T("btn_add_wallet"), Unique: "AddWallet"}
	bots.Handle(&addBtn, func(c tb.Context) error {
		chains, err := getEnabledSupportedNetworks()
		if err != nil {
			return c.Send(lang.T("read_chains_fail") + err.Error())
		}
		if len(chains) == 0 {
			return c.Send(lang.T("no_available_chains"))
		}

		rows := make([][]tb.InlineButton, 0, len(chains))
		for _, network := range chains {
			btn := tb.InlineButton{
				Text:   strings.ToUpper(network),
				Unique: "SelectWalletNetwork",
				Data:   network,
			}
			bots.Handle(&btn, SelectWalletNetwork)
			rows = append(rows, []tb.InlineButton{btn})
		}
		return c.EditOrSend(lang.T("select_network"), &tb.ReplyMarkup{
			InlineKeyboard: rows,
		})
	})
	refreshBtn := tb.InlineButton{Text: lang.T("btn_refresh"), Unique: "WalletRefresh"}
	bots.Handle(&refreshBtn, WalletList)
	btnList = append(btnList, []tb.InlineButton{addBtn, refreshBtn})

	return c.EditOrSend(lang.T("select_wallet_prompt"), &tb.ReplyMarkup{
		InlineKeyboard: btnList,
	})
}

func SelectWalletNetwork(c tb.Context) error {
	network := strings.ToLower(strings.TrimSpace(c.Data()))
	if network == "" {
		return c.Send(lang.T("select_valid_network"))
	}
	if sender := c.Sender(); sender != nil {
		pendingWalletAddressUsers.Store(sender.ID, pendingWalletAddressState{
			RequestedAt: time.Now(),
			Network:     network,
		})
	}
	return c.Send(lang.TF("add_wallet", strings.ToUpper(network)), &tb.ReplyMarkup{
		ForceReply: true,
	})
}

func WalletInfo(c tb.Context) error {
	id := mathutil.MustUint(c.Data())
	tokenInfo, err := data.GetWalletAddressById(id)
	if err != nil {
		return c.Send(err.Error())
	}

	enableBtn := tb.InlineButton{
		Text:   lang.T("btn_enable"),
		Unique: "enableBtn",
		Data:   c.Data(),
	}
	disableBtn := tb.InlineButton{
		Text:   lang.T("btn_disable"),
		Unique: "disableBtn",
		Data:   c.Data(),
	}
	delBtn := tb.InlineButton{
		Text:   lang.T("btn_delete"),
		Unique: "delBtn",
		Data:   c.Data(),
	}
	backBtn := tb.InlineButton{
		Text:   lang.T("btn_back"),
		Unique: "WalletList",
	}

	bots.Handle(&enableBtn, EnableWallet)
	bots.Handle(&disableBtn, DisableWallet)
	bots.Handle(&delBtn, DelWallet)
	bots.Handle(&backBtn, WalletList)

	net := tokenInfo.Network
	if net == "" {
		net = mdb.NetworkTron
	}
	detail := lang.TF("wallet_detail", net, tokenInfo.Address)
	return c.EditOrReply(detail, &tb.ReplyMarkup{InlineKeyboard: [][]tb.InlineButton{
		{
			enableBtn,
			disableBtn,
			delBtn,
		},
		{
			backBtn,
		},
	}})
}

func EnableWallet(c tb.Context) error {
	id := mathutil.MustUint(c.Data())
	if id <= 0 {
		return c.Send(lang.T("invalid_request"))
	}
	err := data.ChangeWalletAddressStatus(id, mdb.TokenStatusEnable)
	if err != nil {
		return c.Send(err.Error())
	}
	return WalletList(c)
}

func DisableWallet(c tb.Context) error {
	id := mathutil.MustUint(c.Data())
	if id <= 0 {
		return c.Send(lang.T("invalid_request"))
	}
	err := data.ChangeWalletAddressStatus(id, mdb.TokenStatusDisable)
	if err != nil {
		return c.Send(err.Error())
	}
	return WalletList(c)
}

func DelWallet(c tb.Context) error {
	id := mathutil.MustUint(c.Data())
	if id <= 0 {
		return c.Send(lang.T("invalid_request"))
	}
	err := data.DeleteWalletAddressById(id)
	if err != nil {
		return c.Send(err.Error())
	}
	return WalletList(c)
}

func isWalletAddressPending(userID int64) bool {
	if userID <= 0 {
		return false
	}
	value, ok := pendingWalletAddressUsers.Load(userID)
	if !ok {
		return false
	}

	state, ok := value.(pendingWalletAddressState)
	if !ok || time.Since(state.RequestedAt) > pendingWalletAddressTTL {
		pendingWalletAddressUsers.Delete(userID)
		return false
	}
	return true
}

func getPendingWalletAddressState(userID int64) (pendingWalletAddressState, bool) {
	v, ok := pendingWalletAddressUsers.Load(userID)
	if !ok {
		return pendingWalletAddressState{}, false
	}
	state, ok := v.(pendingWalletAddressState)
	return state, ok
}

func getEnabledSupportedNetworks() ([]string, error) {
	chains, err := data.ListEnabledChains()
	if err != nil {
		return nil, err
	}
	networks := make([]string, 0, len(chains))
	for _, ch := range chains {
		if n := strings.ToLower(strings.TrimSpace(ch.Network)); n != "" {
			networks = append(networks, n)
		}
	}
	sort.Strings(networks)
	return networks, nil
}
