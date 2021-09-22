package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/LightningTipBot/LightningTipBot/internal/lnbits"
	"github.com/LightningTipBot/LightningTipBot/internal/runtime"
	"github.com/LightningTipBot/LightningTipBot/internal/storage"
	"github.com/LightningTipBot/LightningTipBot/pkg/lightning"
	log "github.com/sirupsen/logrus"
	tb "gopkg.in/tucnak/telebot.v2"
	"strings"
)

const (
	sendValidAmountMessage     = "Did you enter a valid amount?"
	sendUserHasNoWalletMessage = "🚫 User %s hasn't created a wallet yet."
	sendSentMessage            = "💸 %d sat sent to %s."
	sendPublicSentMessage      = "💸 %d sat sent from %s to %s."
	sendReceivedMessage        = "🏅 %s sent you %d sat."
	sendErrorMessage           = "🚫 Send failed."
	confirmSendMessage         = "Do you want to pay to %s?\n\n💸 Amount: %d sat"
	confirmSendAppendMemo      = "\n✉️ %s"
	sendCancelledMessage       = "🚫 Send cancelled."
	errorTryLaterMessage       = "🚫 Error. Please try again later."
	sendHelpText               = "📖 Oops, that didn't work. %s\n\n" +
		"*Usage:* `/send <amount> <user> [<memo>]`\n" +
		"*Example:* `/send 1000 @LightningTipBot I just like the bot ❤️`\n" +
		"*Example:* `/send 1234 LightningTipBot@ln.tips`"
)

var (
	sendConfirmationMenu = &tb.ReplyMarkup{ResizeReplyKeyboard: true}
	btnCancelSend        = sendConfirmationMenu.Data("🚫 Cancel", "cancel_send")
	btnSend              = sendConfirmationMenu.Data("✅ Send", "confirm_send")
)

func helpSendUsage(errormsg string) string {
	if len(errormsg) > 0 {
		return fmt.Sprintf(sendHelpText, fmt.Sprintf("%s", errormsg))
	} else {
		return fmt.Sprintf(sendHelpText, "")
	}
}

func (bot *TipBot) SendCheckSyntax(m *tb.Message) (bool, string) {
	arguments := strings.Split(m.Text, " ")
	if len(arguments) < 2 {
		return false, fmt.Sprintf("Did you enter an amount and a recipient? You can use the /send command to either send to Telegram users like %s or to a Lightning address like LightningTipBot@ln.tips.", GetUserStrMd(bot.telegram.Me))
	}
	return true, ""
}

type SendData struct {
	*storage.Transaction
	From           *lnbits.User `json:"from"`
	ToTelegramId   int          `json:"to_telegram_id"`
	ToTelegramUser string       `json:"to_telegram_user"`
	Memo           string       `json:"memo"`
	Message        string       `json:"message"`
	Amount         int64        `json:"amount"`
}

func NewSend() *SendData {
	sendData := &SendData{
		Transaction: &storage.Transaction{
			Active:        true,
			InTransaction: false,
		},
	}
	return sendData
}

// sendHandler invoked on "/send 123 @user" command
func (bot *TipBot) sendHandler(ctx context.Context, m *tb.Message) {
	bot.anyTextHandler(ctx, m)
	user := LoadUser(ctx)
	if user.Wallet == nil {
		return
	}

	// reset state immediately
	ResetUserState(user, *bot)

	// check and print all commands

	// If the send is a reply, then trigger /tip handler
	if m.IsReply() {
		bot.tipHandler(ctx, m)
		return
	}

	if ok, errstr := bot.SendCheckSyntax(m); !ok {
		bot.trySendMessage(m.Sender, helpSendUsage(errstr))
		NewMessage(m, WithDuration(0, bot.telegram))
		return
	}

	// get send amount, returns 0 if no amount is given
	amount, err := decodeAmountFromCommand(m.Text)
	// info: /send 10 <user> DEMANDS an amount, while /send <ln@address.com> also works without
	// todo: /send <user> should also invoke amount input dialog if no amount is given

	// CHECK whether first or second argument is a LIGHTNING ADDRESS
	arg := ""
	if len(strings.Split(m.Text, " ")) > 2 {
		arg, err = getArgumentFromCommand(m.Text, 2)
	} else if len(strings.Split(m.Text, " ")) == 2 {
		arg, err = getArgumentFromCommand(m.Text, 1)
	}
	if err == nil {
		if lightning.IsLightningAddress(arg) {
			// lightning address, send to that address
			err = bot.sendToLightningAddress(ctx, m, arg, amount)
			if err != nil {
				log.Errorln(err.Error())
				return
			}
			return
		}
	}

	// todo: this error might have been overwritten by the functions above
	// we should only check for a valid amount here, instead of error and amount

	// ASSUME INTERNAL SEND TO TELEGRAM USER
	if err != nil || amount < 1 {
		errmsg := fmt.Sprintf("[/send] Error: Send amount not valid.")
		log.Errorln(errmsg)
		// immediately delete if the amount is bullshit
		NewMessage(m, WithDuration(0, bot.telegram))
		bot.trySendMessage(m.Sender, helpSendUsage(sendValidAmountMessage))
		return
	}

	// SEND COMMAND IS VALID
	// check for memo in command
	sendMemo := GetMemoFromCommand(m.Text, 3)

	toUserStrMention := ""
	toUserStrWithoutAt := ""

	// check for user in command, accepts user mention or plan username without @
	if len(m.Entities) > 1 && m.Entities[1].Type == "mention" {
		toUserStrMention = m.Text[m.Entities[1].Offset : m.Entities[1].Offset+m.Entities[1].Length]
		toUserStrWithoutAt = strings.TrimPrefix(toUserStrMention, "@")
	} else {
		toUserStrWithoutAt, err = getArgumentFromCommand(m.Text, 2)
		if err != nil {
			log.Errorln(err.Error())
			return
		}
		toUserStrMention = "@" + toUserStrWithoutAt
		toUserStrWithoutAt = strings.TrimPrefix(toUserStrWithoutAt, "@")
	}

	err = bot.parseCmdDonHandler(ctx, m)
	if err == nil {
		return
	}

	toUserDb, err := GetUserByTelegramUsername(toUserStrWithoutAt, *bot)
	if err != nil {
		NewMessage(m, WithDuration(0, bot.telegram))
		bot.trySendMessage(m.Sender, fmt.Sprintf(sendUserHasNoWalletMessage, toUserStrMention))
		return
	}

	// entire text of the inline object
	confirmText := fmt.Sprintf(confirmSendMessage, MarkdownEscape(toUserStrMention), amount)
	if len(sendMemo) > 0 {
		confirmText = confirmText + fmt.Sprintf(confirmSendAppendMemo, MarkdownEscape(sendMemo))
	}
	// object that holds all information about the send payment
	id := fmt.Sprintf("send-%d-%d-%s", m.Sender.ID, amount, RandStringRunes(5))
	sendData := SendData{
		From: user,
		Transaction: &storage.Transaction{
			ID:            id,
			Active:        true,
			InTransaction: false,
		},
		Amount:         int64(amount),
		ToTelegramId:   toUserDb.Telegram.ID,
		ToTelegramUser: toUserStrWithoutAt,
		Memo:           sendMemo,
		Message:        confirmText,
	}
	// add result to persistent struct
	runtime.IgnoreError(bot.bunt.Set(sendData))

	sendDataJson, err := json.Marshal(sendData)
	if err != nil {
		NewMessage(m, WithDuration(0, bot.telegram))
		log.Printf("[/send] Error: %s\n", err.Error())
		bot.trySendMessage(m.Sender, fmt.Sprint(errorTryLaterMessage))
		return
	}
	// save the send data to the database
	// log.Debug(sendData)
	SetUserState(user, *bot, lnbits.UserStateConfirmSend, string(sendDataJson))

	btnSend.Data = id
	btnCancelSend.Data = id

	sendConfirmationMenu.Inline(sendConfirmationMenu.Row(btnSend, btnCancelSend))

	if m.Private() {
		bot.trySendMessage(m.Chat, confirmText, sendConfirmationMenu)
	} else {
		bot.tryReplyMessage(m, confirmText, sendConfirmationMenu)
	}
}

// sendHandler invoked when user clicked send on payment confirmation
func (bot *TipBot) confirmSendHandler(ctx context.Context, c *tb.Callback) {
	tx := NewSend()
	tx.ID = c.Data
	sn, err := storage.GetTransaction(tx, tx.Transaction, bot.bunt)
	if err != nil {
		log.Errorf("[acceptSendHandler] %s", err)
		return
	}
	sendData := sn.(*SendData)
	// onnly the correct user can press
	if sendData.From.Telegram.ID != c.Sender.ID {
		return
	}
	// immediatelly set intransaction to block duplicate calls
	err = storage.Lock(sendData, sendData.Transaction, bot.bunt)
	if err != nil {
		log.Errorf("[acceptSendHandler] %s", err)
		bot.tryDeleteMessage(c.Message)
		return
	}
	if !sendData.Active {
		log.Errorf("[acceptSendHandler] send not active anymore")
		bot.tryDeleteMessage(c.Message)
		return
	}
	defer storage.Release(sendData, sendData.Transaction, bot.bunt)

	// // remove buttons from confirmation message
	// bot.tryEditMessage(c.Message, MarkdownEscape(sendData.Message), &tb.ReplyMarkup{})

	// decode callback data
	// log.Debug("[send] Callback: %s", c.Data)
	from := LoadUser(ctx)
	ResetUserState(from, *bot) // we don't need to check the statekey anymore like we did earlier

	// information about the send
	toId := sendData.ToTelegramId
	toUserStrWithoutAt := sendData.ToTelegramUser
	amount := sendData.Amount
	sendMemo := sendData.Memo

	// we can now get the wallets of both users
	to, err := GetUser(&tb.User{ID: toId, Username: toUserStrWithoutAt}, *bot)
	if err != nil {
		log.Errorln(err.Error())
		bot.tryDeleteMessage(c.Message)
		return
	}
	toUserStrMd := GetUserStrMd(to.Telegram)
	fromUserStrMd := GetUserStrMd(from.Telegram)
	toUserStr := GetUserStr(to.Telegram)
	fromUserStr := GetUserStr(from.Telegram)

	transactionMemo := fmt.Sprintf("Send from %s to %s (%d sat).", fromUserStr, toUserStr, amount)
	t := NewTransaction(bot, from, to, int(amount), TransactionType("send"))
	t.Memo = transactionMemo

	success, err := t.Send()
	if !success || err != nil {
		// bot.trySendMessage(c.Sender, sendErrorMessage)
		errmsg := fmt.Sprintf("[/send] Error: Transaction failed. %s", err)
		log.Errorln(errmsg)
		bot.tryEditMessage(c.Message, sendErrorMessage, &tb.ReplyMarkup{})
		return
	}

	sendData.InTransaction = false

	bot.trySendMessage(to.Telegram, fmt.Sprintf(sendReceivedMessage, fromUserStrMd, amount))
	// bot.trySendMessage(from.Telegram, fmt.Sprintf(sendSentMessage, amount, toUserStrMd))
	if c.Message.Private() {
		bot.tryEditMessage(c.Message, fmt.Sprintf(sendSentMessage, amount, toUserStrMd), &tb.ReplyMarkup{})
	} else {
		bot.trySendMessage(c.Sender, fmt.Sprintf(sendSentMessage, amount, toUserStrMd))
		bot.tryEditMessage(c.Message, fmt.Sprintf(sendPublicSentMessage, amount, fromUserStrMd, toUserStrMd), &tb.ReplyMarkup{})
	}
	// send memo if it was present
	if len(sendMemo) > 0 {
		bot.trySendMessage(to.Telegram, fmt.Sprintf("✉️ %s", MarkdownEscape(sendMemo)))
	}

	return
}

// cancelPaymentHandler invoked when user clicked cancel on payment confirmation
func (bot *TipBot) cancelSendHandler(ctx context.Context, c *tb.Callback) {
	// reset state immediately
	user := LoadUser(ctx)
	ResetUserState(user, *bot)
	tx := NewSend()
	tx.ID = c.Data
	sn, err := storage.GetTransaction(tx, tx.Transaction, bot.bunt)
	if err != nil {
		log.Errorf("[acceptSendHandler] %s", err)
		return
	}
	sendData := sn.(*SendData)
	// onnly the correct user can press
	if sendData.From.Telegram.ID != c.Sender.ID {
		return
	}
	// remove buttons from confirmation message
	bot.tryEditMessage(c.Message, sendCancelledMessage, &tb.ReplyMarkup{})
	sendData.InTransaction = false
	storage.Inactivate(sendData, sendData.Transaction, bot.bunt)
	// // delete the confirmation message
	// bot.tryDeleteMessage(c.Message)
	// // notify the user
	// bot.trySendMessage(c.Sender, sendCancelledMessage)

	// // set the inlineSend inactive
	// sendData.Active = false
	// sendData.InTransaction = false
	// runtime.IgnoreError(bot.bunt.Set(sendData))

}
