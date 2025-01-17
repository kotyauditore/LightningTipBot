package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/url"
	"strconv"
	"strings"

	"github.com/LightningTipBot/LightningTipBot/internal/lnbits"
	"github.com/LightningTipBot/LightningTipBot/internal/runtime"
	"github.com/LightningTipBot/LightningTipBot/internal/storage/transaction"
	lnurl "github.com/fiatjaf/go-lnurl"
	log "github.com/sirupsen/logrus"
	tb "gopkg.in/tucnak/telebot.v2"
)

// LnurlPayState saves the state of the user for an LNURL payment
type LnurlPayState struct {
	*transaction.Base
	From              *lnbits.User            `json:"from"`
	LNURLPayResponse1 lnurl.LNURLPayResponse1 `json:"LNURLPayResponse1"`
	LNURLPayResponse2 lnurl.LNURLPayResponse2 `json:"LNURLPayResponse2"`
	Amount            int                     `json:"amount"`
	Comment           string                  `json:"comment"`
	LanguageCode      string                  `json:"languagecode"`
}

// lnurlPayHandler1 is invoked when the first lnurl response was a lnurlpay response
// at this point, the user hans't necessarily entered an amount yet
func (bot *TipBot) lnurlPayHandler(ctx context.Context, m *tb.Message, payParams LnurlPayState) {
	user := LoadUser(ctx)
	if user.Wallet == nil {
		return
	}
	// object that holds all information about the send payment
	id := fmt.Sprintf("lnurlp-%d-%s", m.Sender.ID, RandStringRunes(5))
	lnurlPayState := LnurlPayState{
		Base:              transaction.New(transaction.ID(id)),
		LNURLPayResponse1: payParams.LNURLPayResponse1,
		LanguageCode:      ctx.Value("publicLanguageCode").(string),
	}
	// add result to persistent struct
	runtime.IgnoreError(lnurlPayState.Set(lnurlPayState, bot.Bunt))

	// if no amount is in the command, ask for it
	amount, err := decodeAmountFromCommand(m.Text)
	if err != nil || amount < 1 {
		// // no amount was entered, set user state and ask for amount
		bot.askForAmount(ctx, id, "LnurlPayState", lnurlPayState.LNURLPayResponse1.MinSendable, lnurlPayState.LNURLPayResponse1.MaxSendable, m.Text)
		return
	}

	// amount is already present in the command, i.e., /lnurl <amount> <LNURL>
	// amount not in allowed range from LNURL
	if int64(amount) > (lnurlPayState.LNURLPayResponse1.MaxSendable/1000) || int64(amount) < (lnurlPayState.LNURLPayResponse1.MinSendable/1000) {
		err = fmt.Errorf("amount not in range")
		log.Warnf("[lnurlPayHandler] Error: %s", err.Error())
		bot.trySendMessage(m.Sender, fmt.Sprintf(Translate(ctx, "lnurlInvalidAmountRangeMessage"), lnurlPayState.LNURLPayResponse1.MinSendable/1000, lnurlPayState.LNURLPayResponse1.MaxSendable/1000))
		ResetUserState(user, bot)
		return
	}
	// set also amount in the state of the user
	lnurlPayState.Amount = amount * 1000 // save as mSat

	// check if comment is presentin lnrul-p
	memo := GetMemoFromCommand(m.Text, 3)
	// shorten comment to allowed length
	if len(memo) > int(lnurlPayState.LNURLPayResponse1.CommentAllowed) {
		memo = memo[:lnurlPayState.LNURLPayResponse1.CommentAllowed]
	}
	if len(memo) > 0 {
		lnurlPayState.Comment = memo
	}

	// add result to persistent struct
	runtime.IgnoreError(lnurlPayState.Set(lnurlPayState, bot.Bunt))

	// not necessary to save this in the state data, but till doing it
	paramsJson, err := json.Marshal(lnurlPayState)
	if err != nil {
		log.Errorf("[lnurlPayHandler] Error: %s", err.Error())
		// bot.trySendMessage(m.Sender, err.Error())
		return
	}
	SetUserState(user, bot, lnbits.UserHasEnteredAmount, string(paramsJson))
	// directly go to confirm
	bot.lnurlPayHandlerSend(ctx, m)
	return
}

// lnurlPayHandler is invoked when the user has delivered an amount and is ready to pay
func (bot *TipBot) lnurlPayHandlerSend(ctx context.Context, m *tb.Message) {
	user := LoadUser(ctx)
	if user.Wallet == nil {
		return
	}
	statusMsg := bot.trySendMessage(m.Sender, Translate(ctx, "lnurlGettingUserMessage"))

	// assert that user has entered an amount
	if user.StateKey != lnbits.UserHasEnteredAmount {
		log.Errorln("[lnurlPayHandler] state keys don't match")
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return
	}

	// read the enter amount state from user.StateData
	var enterAmountData EnterAmountStateData
	err := json.Unmarshal([]byte(user.StateData), &enterAmountData)
	if err != nil {
		log.Errorf("[lnurlPayHandler] Error: %s", err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return
	}

	// use the enter amount state of the user to load the LNURL payment state
	tx := &LnurlPayState{Base: transaction.New(transaction.ID(enterAmountData.ID))}
	fn, err := tx.Get(tx, bot.Bunt)
	if err != nil {
		log.Errorf("[lnurlPayHandler] Error: %s", err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return
	}
	lnurlPayState := fn.(*LnurlPayState)

	// LnurlPayState loaded

	client, err := bot.GetHttpClient()
	if err != nil {
		log.Errorf("[lnurlPayHandler] Error: %s", err.Error())
		// bot.trySendMessage(c.Sender, err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return
	}
	callbackUrl, err := url.Parse(lnurlPayState.LNURLPayResponse1.Callback)
	if err != nil {
		log.Errorf("[lnurlPayHandler] Error: %s", err.Error())
		// bot.trySendMessage(c.Sender, err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return
	}
	qs := callbackUrl.Query()
	// add amount to query string
	qs.Set("amount", strconv.Itoa(lnurlPayState.Amount)) // msat
	// add comment to query string
	if len(lnurlPayState.Comment) > 0 {
		qs.Set("comment", lnurlPayState.Comment)
	}

	callbackUrl.RawQuery = qs.Encode()

	res, err := client.Get(callbackUrl.String())
	if err != nil {
		log.Errorf("[lnurlPayHandlerSend] Error: %s", err.Error())
		// bot.trySendMessage(c.Sender, err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Errorf("[lnurlPayHandlerSend] Error: %s", err.Error())
		// bot.trySendMessage(c.Sender, err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return
	}

	var response2 lnurl.LNURLPayResponse2
	json.Unmarshal(body, &response2)
	if response2.Status == "ERROR" || len(response2.PR) < 1 {
		error_reason := "Could not receive invoice."
		if len(response2.Reason) > 0 {
			error_reason = response2.Reason
		}
		log.Errorf("[lnurlPayHandler] Error in LNURLPayResponse2: %s", error_reason)
		bot.tryEditMessage(statusMsg, fmt.Sprintf(Translate(ctx, "lnurlPaymentFailed"), error_reason))
		return
	}

	lnurlPayState.LNURLPayResponse2 = response2
	// add result to persistent struct
	runtime.IgnoreError(lnurlPayState.Set(lnurlPayState, bot.Bunt))
	bot.Telegram.Delete(statusMsg)
	m.Text = fmt.Sprintf("/pay %s", response2.PR)
	bot.payHandler(ctx, m)
}

func (bot *TipBot) sendToLightningAddress(ctx context.Context, m *tb.Message, address string, amount int) error {
	split := strings.Split(address, "@")
	if len(split) != 2 {
		return fmt.Errorf("lightning address format wrong")
	}
	host := strings.ToLower(split[1])
	name := strings.ToLower(split[0])

	// convert address scheme into LNURL Bech32 format
	callback := fmt.Sprintf("https://%s/.well-known/lnurlp/%s", host, name)

	log.Infof("[sendToLightningAddress] %s: callback: %s", GetUserStr(m.Sender), callback)

	lnurl, err := lnurl.LNURLEncode(callback)
	if err != nil {
		return err
	}

	if amount > 0 {
		// only when amount is given, we will also add a comment to the command
		// we do this because if the amount is not given, we will have to ask for it later
		// in the lnurl handler and we don't want to add another step where we ask for a comment
		// the command to pay to lnurl with comment is /lnurl <amount> <lnurl> <comment>
		// check if comment is presentin lnrul-p
		memo := GetMemoFromCommand(m.Text, 3)
		m.Text = fmt.Sprintf("/lnurl %d %s", amount, lnurl)
		// shorten comment to allowed length
		if len(memo) > 0 {
			m.Text = m.Text + " " + memo
		}
	} else {
		// no amount was given so we will just send the lnurl
		// this will invoke the "enter amount" dialog in the lnurl handler
		m.Text = fmt.Sprintf("/lnurl %s", lnurl)
	}
	bot.lnurlHandler(ctx, m)
	return nil
}
