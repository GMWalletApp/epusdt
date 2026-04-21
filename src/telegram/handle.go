package telegram

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/util/walletaddr"
	"github.com/gookit/goutil/mathutil"
	"github.com/gookit/goutil/strutil"
	tb "gopkg.in/telebot.v3"
)

const (
	ReplaySelectNetwork     = "请选择要添加的钱包网络"
	ReplayAddWallet         = "请发送 %s 网络收款地址"
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
		_ = c.Send("请先选择网络，再发送地址。")
		return nil
	}

	var err error
	if !walletaddr.Validate(state.Network, msgText) {
		_ = c.Send(fmt.Sprintf("钱包 [%s] 添加失败：不是合法的 %s 地址", msgText, strings.ToUpper(state.Network)))
		return nil
	}
	storeAddress := walletaddr.Normalize(state.Network, msgText)
	_, err = data.AddWalletAddressWithNetwork(state.Network, storeAddress)
	if err != nil {
		return c.Send(err.Error())
	}
	pendingWalletAddressUsers.Delete(senderID)

	_ = c.Send(fmt.Sprintf("钱包 [%s] 添加成功（%s）", storeAddress, strings.ToUpper(state.Network)))
	return WalletList(c)
}

func WalletList(c tb.Context) error {
	wallets, err := data.GetAllWalletAddress()
	if err != nil {
		return err
	}

	var btnList [][]tb.InlineButton
	for _, wallet := range wallets {
		status := "已启用✅"
		if wallet.Status == mdb.TokenStatusDisable {
			status = "已禁用🚫"
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

	addBtn := tb.InlineButton{Text: "添加钱包地址", Unique: "AddWallet"}
	bots.Handle(&addBtn, func(c tb.Context) error {
		chains, err := getEnabledSupportedNetworks()
		if err != nil {
			return c.Send("读取支持链失败：" + err.Error())
		}
		if len(chains) == 0 {
			return c.Send("当前没有可用链，请先在后台配置 supported-assets。")
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
		return c.EditOrSend(ReplaySelectNetwork, &tb.ReplyMarkup{
			InlineKeyboard: rows,
		})
	})
	btnList = append(btnList, []tb.InlineButton{addBtn})

	return c.EditOrSend("请选择钱包继续操作", &tb.ReplyMarkup{
		InlineKeyboard: btnList,
	})
}

func SelectWalletNetwork(c tb.Context) error {
	network := strings.ToLower(strings.TrimSpace(c.Data()))
	if network == "" {
		return c.Send("请选择有效网络")
	}
	if sender := c.Sender(); sender != nil {
		pendingWalletAddressUsers.Store(sender.ID, pendingWalletAddressState{
			RequestedAt: time.Now(),
			Network:     network,
		})
	}
	return c.Send(fmt.Sprintf(ReplayAddWallet, strings.ToUpper(network)), &tb.ReplyMarkup{
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
		Text:   "启用",
		Unique: "enableBtn",
		Data:   c.Data(),
	}
	disableBtn := tb.InlineButton{
		Text:   "禁用",
		Unique: "disableBtn",
		Data:   c.Data(),
	}
	delBtn := tb.InlineButton{
		Text:   "删除",
		Unique: "delBtn",
		Data:   c.Data(),
	}
	backBtn := tb.InlineButton{
		Text:   "返回",
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
	detail := fmt.Sprintf("网络：%s\n地址：%s", net, tokenInfo.Address)
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
		return c.Send("请求不合法！")
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
		return c.Send("请求不合法！")
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
		return c.Send("请求不合法！")
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
	assets, err := data.ListEnabledSupportedAssets()
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, a := range assets {
		n := strings.ToLower(strings.TrimSpace(a.Network))
		if n == "" {
			continue
		}
		set[n] = struct{}{}
	}
	networks := make([]string, 0, len(set))
	for n := range set {
		networks = append(networks, n)
	}
	sort.Strings(networks)
	return networks, nil
}
