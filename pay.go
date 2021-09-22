package main

import (
	"context"
	"fmt"
	"github.com/LightningTipBot/LightningTipBot/internal/storage"
	log "github.com/sirupsen/logrus"
	"strings"

	"github.com/LightningTipBot/LightningTipBot/internal/lnbits"
	"github.com/LightningTipBot/LightningTipBot/internal/runtime"
	decodepay "github.com/fiatjaf/ln-decodepay"
	tb "gopkg.in/tucnak/telebot.v2"
)

const (
	paymentCancelledMessage  = "🚫 Payment cancelled."
	invoicePaidMessage       = "⚡️ Payment sent."
	invoicePublicPaidMessage = "⚡️ Payment sent by %s."
	// invoicePrivateChatOnlyErrorMessage = "You can pay invoices only in the private chat with the bot."
	invalidInvoiceHelpMessage    = "Did you enter a valid Lightning invoice? Try /send if you want to send to a Telegram user or Lightning address."
	invoiceNoAmountMessage       = "🚫 Can't pay invoices without an amount."
	insufficientFundsMessage     = "🚫 Insufficient funds. You have %d sat but you need at least %d sat."
	feeReserveMessage            = "⚠️ Sending your entire balance might fail because of network fees. If it fails, try sending a bit less."
	invoicePaymentFailedMessage  = "🚫 Payment failed: %s"
	invoiceUndefinedErrorMessage = "Could not pay invoice."
	confirmPayInvoiceMessage     = "Do you want to send this payment?\n\n💸 Amount: %d sat"
	confirmPayAppendMemo         = "\n✉️ %s"
	payHelpText                  = "📖 Oops, that didn't work. %s\n\n" +
		"*Usage:* `/pay <invoice>`\n" +
		"*Example:* `/pay lnbc20n1psscehd...`"
)

var (
	paymentConfirmationMenu = &tb.ReplyMarkup{ResizeReplyKeyboard: true}
	btnCancelPay            = paymentConfirmationMenu.Data("🚫 Cancel", "cancel_pay")
	btnPay                  = paymentConfirmationMenu.Data("✅ Pay", "confirm_pay")
)

func helpPayInvoiceUsage(errormsg string) string {
	if len(errormsg) > 0 {
		return fmt.Sprintf(payHelpText, fmt.Sprintf("%s", errormsg))
	} else {
		return fmt.Sprintf(payHelpText, "")
	}
}

type PayData struct {
	*storage.Transaction
	From    *lnbits.User `json:"from"`
	Invoice string       `json:"invoice"`
	Hash    string       `json:"hash"`
	Proof   string       `json:"proof"`
	Memo    string       `json:"memo"`
	Message string       `json:"message"`
	Amount  int64        `json:"amount"`
}

func NewPay() *PayData {
	payData := &PayData{
		Transaction: &storage.Transaction{
			Active:        true,
			InTransaction: false,
		},
	}
	return payData
}

// payHandler invoked on "/pay lnbc..." command
func (bot TipBot) payHandler(ctx context.Context, m *tb.Message) {
	// check and print all commands
	bot.anyTextHandler(ctx, m)
	user := LoadUser(ctx)
	if user.Wallet == nil {
		return
	}

	// if m.Chat.Type != tb.ChatPrivate {
	// 	// delete message
	// 	NewMessage(m, WithDuration(0, bot.telegram))
	// 	bot.trySendMessage(m.Sender, helpPayInvoiceUsage(invoicePrivateChatOnlyErrorMessage))
	// 	return
	// }
	if len(strings.Split(m.Text, " ")) < 2 {
		NewMessage(m, WithDuration(0, bot.telegram))
		bot.trySendMessage(m.Sender, helpPayInvoiceUsage(""))
		return
	}
	userStr := GetUserStr(m.Sender)
	paymentRequest, err := getArgumentFromCommand(m.Text, 1)
	if err != nil {
		NewMessage(m, WithDuration(0, bot.telegram))
		bot.trySendMessage(m.Sender, helpPayInvoiceUsage(invalidInvoiceHelpMessage))
		errmsg := fmt.Sprintf("[/pay] Error: Could not getArgumentFromCommand: %s", err)
		log.Errorln(errmsg)
		return
	}
	paymentRequest = strings.ToLower(paymentRequest)
	// get rid of the URI prefix
	paymentRequest = strings.TrimPrefix(paymentRequest, "lightning:")

	// decode invoice
	bolt11, err := decodepay.Decodepay(paymentRequest)
	if err != nil {
		bot.trySendMessage(m.Sender, helpPayInvoiceUsage(invalidInvoiceHelpMessage))
		errmsg := fmt.Sprintf("[/pay] Error: Could not decode invoice: %s", err)
		log.Errorln(errmsg)
		return
	}
	amount := int(bolt11.MSatoshi / 1000)

	if amount <= 0 {
		bot.trySendMessage(m.Sender, invoiceNoAmountMessage)
		errmsg := fmt.Sprint("[/pay] Error: invoice without amount")
		log.Errorln(errmsg)
		return
	}

	// check user balance first
	balance, err := bot.GetUserBalance(user)
	if err != nil {
		NewMessage(m, WithDuration(0, bot.telegram))
		errmsg := fmt.Sprintf("[/pay] Error: Could not get user balance: %s", err)
		log.Errorln(errmsg)
		return
	}
	if amount > balance {
		NewMessage(m, WithDuration(0, bot.telegram))
		bot.trySendMessage(m.Sender, fmt.Sprintf(insufficientFundsMessage, balance, amount))
		return
	}
	// send warning that the invoice might fail due to missing fee reserve
	if float64(amount) > float64(balance)*0.99 {
		bot.trySendMessage(m.Sender, feeReserveMessage)
	}

	confirmText := fmt.Sprintf(confirmPayInvoiceMessage, amount)
	if len(bolt11.Description) > 0 {
		confirmText = confirmText + fmt.Sprintf(confirmPayAppendMemo, MarkdownEscape(bolt11.Description))
	}

	log.Printf("[/pay] User: %s, amount: %d sat.", userStr, amount)

	// object that holds all information about the send payment
	id := fmt.Sprintf("pay-%d-%d-%s", m.Sender.ID, amount, RandStringRunes(5))
	payData := PayData{
		From:    user,
		Invoice: paymentRequest,
		Transaction: &storage.Transaction{
			Active:        true,
			InTransaction: false,
			ID:            id,
		},
		Amount:  int64(amount),
		Memo:    bolt11.Description,
		Message: confirmText,
	}
	// add result to persistent struct
	runtime.IgnoreError(bot.bunt.Set(payData))

	SetUserState(user, bot, lnbits.UserStateConfirmPayment, paymentRequest)

	// // // create inline buttons
	btnPay.Data = id
	btnCancelPay.Data = id
	paymentConfirmationMenu.Inline(paymentConfirmationMenu.Row(btnPay, btnCancelPay))
	bot.trySendMessage(m.Chat, confirmText, paymentConfirmationMenu)
}

// confirmPayHandler when user clicked pay on payment confirmation
func (bot TipBot) confirmPayHandler(ctx context.Context, c *tb.Callback) {
	tx := NewPay()
	tx.ID = c.Data
	sn, err := storage.GetTransaction(tx, tx.Transaction, bot.bunt)
	// immediatelly set intransaction to block duplicate calls
	if err != nil {
		log.Errorf("[confirmPayHandler] %s", err)
		return
	}
	payData := sn.(*PayData)

	// onnly the correct user can press
	if payData.From.Telegram.ID != c.Sender.ID {
		return
	}
	// immediatelly set intransaction to block duplicate calls
	err = storage.Lock(payData, payData.Transaction, bot.bunt)
	if err != nil {
		log.Errorf("[acceptSendHandler] %s", err)
		bot.tryDeleteMessage(c.Message)
		return
	}
	if !payData.Active {
		log.Errorf("[confirmPayHandler] send not active anymore")
		bot.tryDeleteMessage(c.Message)
		return
	}
	defer storage.Release(payData, payData.Transaction, bot.bunt)

	// remove buttons from confirmation message
	// bot.tryEditMessage(c.Message, MarkdownEscape(payData.Message), &tb.ReplyMarkup{})

	user := LoadUser(ctx)
	if user.Wallet == nil {
		bot.tryDeleteMessage(c.Message)
		return
	}

	invoiceString := payData.Invoice

	// reset state immediately
	ResetUserState(user, bot)

	userStr := GetUserStr(c.Sender)
	// pay invoice
	invoice, err := user.Wallet.Pay(lnbits.PaymentParams{Out: true, Bolt11: invoiceString}, bot.client)
	if err != nil {
		errmsg := fmt.Sprintf("[/pay] Could not pay invoice of user %s: %s", userStr, err)
		if len(err.Error()) == 0 {
			err = fmt.Errorf(invoiceUndefinedErrorMessage)
		}
		// bot.trySendMessage(c.Sender, fmt.Sprintf(invoicePaymentFailedMessage, err))
		bot.tryEditMessage(c.Message, fmt.Sprintf(invoicePaymentFailedMessage, err), &tb.ReplyMarkup{})
		log.Errorln(errmsg)
		return
	}
	payData.Hash = invoice.PaymentHash
	payData.InTransaction = false

	if c.Message.Private() {
		bot.tryEditMessage(c.Message, invoicePaidMessage, &tb.ReplyMarkup{})
	} else {
		bot.trySendMessage(c.Sender, invoicePaidMessage)
		bot.tryEditMessage(c.Message, fmt.Sprintf(invoicePublicPaidMessage, userStr), &tb.ReplyMarkup{})
	}
	log.Printf("[/pay] User %s paid invoice %s", userStr, invoice.PaymentHash)
	return
}

// cancelPaymentHandler invoked when user clicked cancel on payment confirmation
func (bot TipBot) cancelPaymentHandler(ctx context.Context, c *tb.Callback) {
	// reset state immediately
	user := LoadUser(ctx)

	ResetUserState(user, bot)
	tx := NewPay()
	tx.ID = c.Data
	sn, err := storage.GetTransaction(tx, tx.Transaction, bot.bunt)
	// immediatelly set intransaction to block duplicate calls
	if err != nil {
		log.Errorf("[cancelPaymentHandler] %s", err)
		return
	}
	payData := sn.(*PayData)
	// onnly the correct user can press
	if payData.From.Telegram.ID != c.Sender.ID {
		return
	}
	bot.tryEditMessage(c.Message, paymentCancelledMessage, &tb.ReplyMarkup{})
	payData.InTransaction = false
	storage.Inactivate(payData, payData.Transaction, bot.bunt)
}
