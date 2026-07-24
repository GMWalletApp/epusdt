package telegram

import (
	"github.com/GMWalletApp/epusdt/lang"
	tb "gopkg.in/telebot.v3"
)

const (
	START_CMD = "/start"
)

var Cmds = []tb.Command{
	{
		Text:        START_CMD,
		Description: lang.T("cmd_start_desc"),
	},
}
