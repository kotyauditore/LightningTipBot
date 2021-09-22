package main

import (
	"context"
	"fmt"
	"github.com/LightningTipBot/LightningTipBot/internal/lnbits"
	"github.com/LightningTipBot/LightningTipBot/internal/storage"
	"strconv"

	"github.com/LightningTipBot/LightningTipBot/internal/runtime"
	log "github.com/sirupsen/logrus"
	tb "gopkg.in/tucnak/telebot.v2"
)

const (
	inlineFaucetMessage                     = "Press ✅ to collect %d sat from this faucet.\n\n🚰 Remaining: %d/%d sat (given to %d/%d users)\n%s"
	inlineFaucetEndedMessage                = "🏅 Faucet empty 🏅\n\n🚰 %d sat given to %d users."
	inlineFaucetAppendMemo                  = "\n✉️ %s"
	inlineFaucetCreateWalletMessage         = "Chat with %s 👈 to manage your wallet."
	inlineFaucetCancelledMessage            = "🚫 Faucet cancelled."
	inlineFaucetInvalidPeruserAmountMessage = "🚫 Peruser amount not divisor of capacity."
	inlineFaucetInvalidAmountMessage        = "🚫 Invalid amount."
	inlineFaucetSentMessage                 = "🚰 %d sat sent to %s."
	inlineFaucetReceivedMessage             = "🚰 %s sent you %d sat."
	inlineFaucetHelpFaucetInGroup           = "Create a faucet in a group with the bot inside or use 👉 inline command (/advanced for more)."
	inlineFaucetHelpText                    = "📖 Oops, that didn't work. %s\n\n" +
		"*Usage:* `/faucet <capacity> <per_user>`\n" +
		"*Example:* `/faucet 210 21`"
)

const (
	inlineQueryFaucetTitle        = "🚰 Create a faucet."
	inlineQueryFaucetDescription  = "Usage: @%s faucet <capacity> <per_user>"
	inlineResultFaucetTitle       = "💸 Create a %d sat faucet."
	inlineResultFaucetDescription = "👉 Click here to create a faucet worth %d sat in this chat."
)

var (
	inlineFaucetMenu      = &tb.ReplyMarkup{ResizeReplyKeyboard: true}
	btnCancelInlineFaucet = inlineFaucetMenu.Data("🚫 Cancel", "cancel_faucet_inline")
	btnAcceptInlineFaucet = inlineFaucetMenu.Data("✅ Collect", "confirm_faucet_inline")
)

type InlineFaucet struct {
	*storage.Transaction
	Message         string       `json:"inline_faucet_message"`
	Amount          int          `json:"inline_faucet_amount"`
	RemainingAmount int          `json:"inline_faucet_remainingamount"`
	PerUserAmount   int          `json:"inline_faucet_peruseramount"`
	From            *lnbits.User `json:"inline_faucet_from"`
	To              []*tb.User   `json:"inline_faucet_to"`
	Memo            string       `json:"inline_faucet_memo"`
	NTotal          int          `json:"inline_faucet_ntotal"`
	NTaken          int          `json:"inline_faucet_ntaken"`
	UserNeedsWallet bool         `json:"inline_faucet_userneedswallet"`
}

func NewInlineFaucet() *InlineFaucet {
	inlineFaucet := &InlineFaucet{
		Message:         "",
		NTaken:          0,
		UserNeedsWallet: false,
		Transaction: &storage.Transaction{
			InTransaction: false,
			Active:        true,
		},
	}
	return inlineFaucet

}

func (bot TipBot) faucetHandler(ctx context.Context, m *tb.Message) {
	if m.Private() {
		bot.trySendMessage(m.Sender, fmt.Sprintf(inlineFaucetHelpText, inlineFaucetHelpFaucetInGroup))
		return
	}
	inlineFaucet := NewInlineFaucet()
	var err error
	inlineFaucet.Amount, err = decodeAmountFromCommand(m.Text)
	if err != nil {
		bot.trySendMessage(m.Sender, fmt.Sprintf(inlineFaucetHelpText, inlineFaucetInvalidAmountMessage))
		bot.tryDeleteMessage(m)
		return
	}
	peruserStr, err := getArgumentFromCommand(m.Text, 2)
	if err != nil {
		bot.trySendMessage(m.Sender, fmt.Sprintf(inlineFaucetHelpText, ""))
		bot.tryDeleteMessage(m)
		return
	}
	inlineFaucet.PerUserAmount, err = strconv.Atoi(peruserStr)
	if err != nil {
		bot.trySendMessage(m.Sender, fmt.Sprintf(inlineFaucetHelpText, inlineFaucetInvalidAmountMessage))
		bot.tryDeleteMessage(m)
		return
	}
	// peruser amount must be >1 and a divisor of amount
	if inlineFaucet.PerUserAmount < 1 || inlineFaucet.Amount%inlineFaucet.PerUserAmount != 0 {
		bot.trySendMessage(m.Sender, fmt.Sprintf(inlineFaucetHelpText, inlineFaucetInvalidPeruserAmountMessage))
		bot.tryDeleteMessage(m)
		return
	}
	inlineFaucet.NTotal = inlineFaucet.Amount / inlineFaucet.PerUserAmount
	fromUser := LoadUser(ctx)
	fromUserStr := GetUserStr(m.Sender)
	balance, err := bot.GetUserBalance(fromUser)
	if err != nil {
		errmsg := fmt.Sprintf("could not get balance of user %s", fromUserStr)
		log.Errorln(errmsg)
		bot.tryDeleteMessage(m)
		return
	}
	// check if fromUser has balance
	if balance < inlineFaucet.Amount {
		log.Errorf("Balance of user %s too low", fromUserStr)
		bot.trySendMessage(m.Sender, fmt.Sprintf(inlineSendBalanceLowMessage, balance))
		bot.tryDeleteMessage(m)
		return
	}

	// // check for memo in command
	memo := GetMemoFromCommand(m.Text, 3)

	inlineMessage := fmt.Sprintf(inlineFaucetMessage, inlineFaucet.PerUserAmount, inlineFaucet.Amount, inlineFaucet.Amount, 0, inlineFaucet.NTotal, MakeProgressbar(inlineFaucet.Amount, inlineFaucet.Amount))
	if len(memo) > 0 {
		inlineMessage = inlineMessage + fmt.Sprintf(inlineFaucetAppendMemo, memo)
	}

	inlineFaucet.ID = fmt.Sprintf("inl-faucet-%d-%d-%s", m.Sender.ID, inlineFaucet.Amount, RandStringRunes(5))

	btnAcceptInlineFaucet.Data = inlineFaucet.ID
	btnCancelInlineFaucet.Data = inlineFaucet.ID
	inlineFaucetMenu.Inline(inlineFaucetMenu.Row(btnAcceptInlineFaucet, btnCancelInlineFaucet))
	bot.trySendMessage(m.Chat, inlineMessage, inlineFaucetMenu)
	log.Infof("[faucet] %s created faucet %s: %d sat (%d per user)", fromUserStr, inlineFaucet.ID, inlineFaucet.Amount, inlineFaucet.PerUserAmount)
	inlineFaucet.Message = inlineMessage
	inlineFaucet.From = fromUser
	inlineFaucet.Memo = memo
	inlineFaucet.RemainingAmount = inlineFaucet.Amount
	runtime.IgnoreError(bot.bunt.Set(inlineFaucet))

}

func (bot TipBot) handleInlineFaucetQuery(ctx context.Context, q *tb.Query) {
	inlineFaucet := NewInlineFaucet()
	var err error
	inlineFaucet.Amount, err = decodeAmountFromCommand(q.Text)
	if err != nil {
		bot.inlineQueryReplyWithError(q, inlineQueryFaucetTitle, fmt.Sprintf(inlineQueryFaucetDescription, bot.telegram.Me.Username))
		return
	}
	if inlineFaucet.Amount < 1 {
		bot.inlineQueryReplyWithError(q, inlineSendInvalidAmountMessage, fmt.Sprintf(inlineQueryFaucetDescription, bot.telegram.Me.Username))
		return
	}

	peruserStr, err := getArgumentFromCommand(q.Text, 2)
	if err != nil {
		bot.inlineQueryReplyWithError(q, inlineQueryFaucetTitle, fmt.Sprintf(inlineQueryFaucetDescription, bot.telegram.Me.Username))
		return
	}
	inlineFaucet.PerUserAmount, err = strconv.Atoi(peruserStr)
	if err != nil {
		bot.inlineQueryReplyWithError(q, inlineQueryFaucetTitle, fmt.Sprintf(inlineQueryFaucetDescription, bot.telegram.Me.Username))
		return
	}
	// peruser amount must be >1 and a divisor of amount
	if inlineFaucet.PerUserAmount < 1 || inlineFaucet.Amount%inlineFaucet.PerUserAmount != 0 {
		bot.inlineQueryReplyWithError(q, inlineFaucetInvalidPeruserAmountMessage, fmt.Sprintf(inlineQueryFaucetDescription, bot.telegram.Me.Username))
		return
	}
	inlineFaucet.NTotal = inlineFaucet.Amount / inlineFaucet.PerUserAmount
	fromUser := LoadUser(ctx)
	fromUserStr := GetUserStr(&q.From)
	balance, err := bot.GetUserBalance(fromUser)
	if err != nil {
		errmsg := fmt.Sprintf("could not get balance of user %s", fromUserStr)
		log.Errorln(errmsg)
		return
	}
	// check if fromUser has balance
	if balance < inlineFaucet.Amount {
		log.Errorf("Balance of user %s too low", fromUserStr)
		bot.inlineQueryReplyWithError(q, fmt.Sprintf(inlineSendBalanceLowMessage, balance), fmt.Sprintf(inlineQueryFaucetDescription, bot.telegram.Me.Username))
		return
	}

	// check for memo in command
	memo := GetMemoFromCommand(q.Text, 3)

	urls := []string{
		queryImage,
	}
	results := make(tb.Results, len(urls)) // []tb.Result
	for i, url := range urls {
		inlineMessage := fmt.Sprintf(inlineFaucetMessage, inlineFaucet.PerUserAmount, inlineFaucet.Amount, inlineFaucet.Amount, 0, inlineFaucet.NTotal, MakeProgressbar(inlineFaucet.Amount, inlineFaucet.Amount))
		if len(memo) > 0 {
			inlineMessage = inlineMessage + fmt.Sprintf(inlineFaucetAppendMemo, memo)
		}
		result := &tb.ArticleResult{
			// URL:         url,
			Text:        inlineMessage,
			Title:       fmt.Sprintf(inlineResultFaucetTitle, inlineFaucet.Amount),
			Description: fmt.Sprintf(inlineResultFaucetDescription, inlineFaucet.Amount),
			// required for photos
			ThumbURL: url,
		}
		id := fmt.Sprintf("inl-faucet-%d-%d-%s", q.From.ID, inlineFaucet.Amount, RandStringRunes(5))
		btnAcceptInlineFaucet.Data = id
		btnCancelInlineFaucet.Data = id
		inlineFaucetMenu.Inline(inlineFaucetMenu.Row(btnAcceptInlineFaucet, btnCancelInlineFaucet))
		result.ReplyMarkup = &tb.InlineKeyboardMarkup{InlineKeyboard: inlineFaucetMenu.InlineKeyboard}
		results[i] = result

		// needed to set a unique string ID for each result
		results[i].SetResultID(id)

		// create persistend inline send struct
		inlineFaucet.Message = inlineMessage
		inlineFaucet.ID = id
		inlineFaucet.From = fromUser
		inlineFaucet.RemainingAmount = inlineFaucet.Amount
		inlineFaucet.Memo = memo
		runtime.IgnoreError(bot.bunt.Set(inlineFaucet))
	}

	err = bot.telegram.Answer(q, &tb.QueryResponse{
		Results:   results,
		CacheTime: 1,
	})
	log.Infof("[faucet] %s created inline faucet %s: %d sat (%d per user)", fromUserStr, inlineFaucet.ID, inlineFaucet.Amount, inlineFaucet.PerUserAmount)
	if err != nil {
		log.Errorln(err)
	}
}

func (bot *TipBot) acceptInlineFaucetHandler(ctx context.Context, c *tb.Callback) {
	to := LoadUser(ctx)
	tx := NewInlineFaucet()
	tx.ID = c.Data
	fn, err := storage.GetTransaction(tx, tx.Transaction, bot.bunt)
	if err != nil {
		log.Errorf("[faucet] %s", err)
		return
	}
	inlineFaucet := fn.(*InlineFaucet)
	from := inlineFaucet.From
	err = storage.Lock(inlineFaucet, inlineFaucet.Transaction, bot.bunt)
	if err != nil {
		log.Errorf("[faucet] %s", err)
		return
	}
	if !inlineFaucet.Active {
		log.Errorf("[faucet] inline send not active anymore")
		return
	}
	// release faucet no matter what
	defer storage.Lock(inlineFaucet, inlineFaucet.Transaction, bot.bunt)

	if from.Telegram.ID == to.Telegram.ID {
		bot.trySendMessage(from.Telegram, sendYourselfMessage)
		return
	}
	// check if to user has already taken from the faucet
	for _, a := range inlineFaucet.To {
		if a.ID == to.Telegram.ID {
			// to user is already in To slice, has taken from facuet
			log.Infof("[faucet] %s already took from faucet %s", GetUserStr(to.Telegram), inlineFaucet.ID)
			return
		}
	}

	if inlineFaucet.RemainingAmount >= inlineFaucet.PerUserAmount {
		toUserStrMd := GetUserStrMd(to.Telegram)
		fromUserStrMd := GetUserStrMd(from.Telegram)
		toUserStr := GetUserStr(to.Telegram)
		fromUserStr := GetUserStr(from.Telegram)
		// check if user exists and create a wallet if not
		_, exists := bot.UserExists(to.Telegram)
		if !exists {
			log.Infof("[faucet] User %s has no wallet.", toUserStr)
			to, err = bot.CreateWalletForTelegramUser(to.Telegram)
			if err != nil {
				errmsg := fmt.Errorf("[faucet] Error: Could not create wallet for %s", toUserStr)
				log.Errorln(errmsg)
				return
			}
		}

		if !to.Initialized {
			inlineFaucet.UserNeedsWallet = true
		}

		// todo: user new get username function to get userStrings
		transactionMemo := fmt.Sprintf("Faucet from %s to %s (%d sat).", fromUserStr, toUserStr, inlineFaucet.PerUserAmount)
		t := NewTransaction(bot, from, to, inlineFaucet.PerUserAmount, TransactionType("faucet"))
		t.Memo = transactionMemo

		success, err := t.Send()
		if !success {
			bot.trySendMessage(from.Telegram, sendErrorMessage)
			errMsg := fmt.Sprintf("[faucet] Transaction failed: %s", err)
			log.Errorln(errMsg)
			return
		}

		log.Infof("[faucet] faucet %s: %d sat from %s to %s ", inlineFaucet.ID, inlineFaucet.PerUserAmount, fromUserStr, toUserStr)
		inlineFaucet.NTaken += 1
		inlineFaucet.To = append(inlineFaucet.To, to.Telegram)
		inlineFaucet.RemainingAmount = inlineFaucet.RemainingAmount - inlineFaucet.PerUserAmount

		_, err = bot.telegram.Send(to.Telegram, fmt.Sprintf(inlineFaucetReceivedMessage, fromUserStrMd, inlineFaucet.PerUserAmount))
		_, err = bot.telegram.Send(from.Telegram, fmt.Sprintf(inlineFaucetSentMessage, inlineFaucet.PerUserAmount, toUserStrMd))
		if err != nil {
			errmsg := fmt.Errorf("[faucet] Error: Send message to %s: %s", toUserStr, err)
			log.Errorln(errmsg)
			return
		}

		// build faucet message
		inlineFaucet.Message = fmt.Sprintf(inlineFaucetMessage, inlineFaucet.PerUserAmount, inlineFaucet.RemainingAmount, inlineFaucet.Amount, inlineFaucet.NTaken, inlineFaucet.NTotal, MakeProgressbar(inlineFaucet.RemainingAmount, inlineFaucet.Amount))
		memo := inlineFaucet.Memo
		if len(memo) > 0 {
			inlineFaucet.Message = inlineFaucet.Message + fmt.Sprintf(inlineFaucetAppendMemo, memo)
		}
		if inlineFaucet.UserNeedsWallet {
			inlineFaucet.Message += "\n\n" + fmt.Sprintf(inlineFaucetCreateWalletMessage, GetUserStrMd(bot.telegram.Me))
		}

		// register new inline buttons
		inlineFaucetMenu = &tb.ReplyMarkup{ResizeReplyKeyboard: true}
		btnCancelInlineFaucet.Data = inlineFaucet.ID
		btnAcceptInlineFaucet.Data = inlineFaucet.ID
		inlineFaucetMenu.Inline(inlineFaucetMenu.Row(btnAcceptInlineFaucet, btnCancelInlineFaucet))
		// update message
		log.Infoln(inlineFaucet.Message)
		bot.tryEditMessage(c.Message, inlineFaucet.Message, inlineFaucetMenu)
	}
	if inlineFaucet.RemainingAmount < inlineFaucet.PerUserAmount {
		// faucet is depleted
		inlineFaucet.Message = fmt.Sprintf(inlineFaucetEndedMessage, inlineFaucet.Amount, inlineFaucet.NTaken)
		if inlineFaucet.UserNeedsWallet {
			inlineFaucet.Message += "\n\n" + fmt.Sprintf(inlineFaucetCreateWalletMessage, GetUserStrMd(bot.telegram.Me))
		}
		bot.tryEditMessage(c.Message, inlineFaucet.Message)
		inlineFaucet.Active = false
	}

}

func (bot *TipBot) cancelInlineFaucetHandler(c *tb.Callback) {
	tx := NewInlineFaucet()
	tx.ID = c.Data
	fn, err := storage.GetTransaction(tx, tx.Transaction, bot.bunt)

	if err != nil {
		log.Errorf("[cancelInlineSendHandler] %s", err)
		return
	}
	inlineFaucet := fn.(*InlineFaucet)
	if c.Sender.ID == inlineFaucet.From.Telegram.ID {
		bot.tryEditMessage(c.Message, inlineFaucetCancelledMessage, &tb.ReplyMarkup{})
		// set the inlineFaucet inactive
		inlineFaucet.Active = false
		inlineFaucet.InTransaction = false
		runtime.IgnoreError(bot.bunt.Set(inlineFaucet))
	}
	return
}
