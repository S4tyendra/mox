package telegram

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/mjl-/bstore"
)

// Session links a Telegram private chat to a mox account.
type Session struct {
	ChatID       int64 `bstore:"noauto"` // Telegram chat id, primary key.
	UserID       int64
	Username     string
	AccountName  string `bstore:"index AccountName"`
	LoginAddress string
	Created      time.Time `bstore:"default now"`
	Notify       bool
}

// MsgRef maps a Telegram message to a stored email, so replies and button
// actions can find the original message.
type MsgRef struct {
	ID            int64
	ChatID        int64 `bstore:"unique ChatID+TelegramMsgID"`
	TelegramMsgID int64
	AccountName   string
	MessageID     int64     // store.Message.ID
	Created       time.Time `bstore:"default now,index"`
}

// PairCode is a short-lived code generated in the account web interface.
type PairCode struct {
	Code         string
	AccountName  string
	LoginAddress string
	Expires      time.Time `bstore:"index"`
}

// ChatInfo is a linked Telegram chat, for the account web API.
type ChatInfo struct {
	ChatID   int64
	Username string
	Created  time.Time
}

func sessionByChat(ctx context.Context, chatID int64) (Session, error) {
	s := Session{ChatID: chatID}
	err := DB.Get(ctx, &s)
	return s, err
}

func sessionsForAccount(ctx context.Context, accountName string) ([]Session, error) {
	return bstore.QueryDB[Session](ctx, DB).FilterNonzero(Session{AccountName: accountName}).List()
}

func saveMsgRef(ctx context.Context, chatID, tgMsgID int64, accountName string, messageID int64) error {
	return DB.Insert(ctx, &MsgRef{
		ChatID:        chatID,
		TelegramMsgID: tgMsgID,
		AccountName:   accountName,
		MessageID:     messageID,
		Created:       time.Now(),
	})
}

func msgRefByTelegram(ctx context.Context, chatID, tgMsgID int64) (MsgRef, error) {
	return bstore.QueryDB[MsgRef](ctx, DB).FilterNonzero(MsgRef{ChatID: chatID, TelegramMsgID: tgMsgID}).Get()
}

const pairAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

func randomPairCode() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	out := make([]byte, 8)
	for i := range b {
		out[i] = pairAlphabet[int(b[i])%len(pairAlphabet)]
	}
	return string(out), nil
}

// CreatePairCode records a pairing code for accountName that expires in 10 minutes.
func CreatePairCode(ctx context.Context, accountName, loginAddress string) (code string, expires time.Time, err error) {
	if DB == nil {
		return "", time.Time{}, ErrNotConfigured
	}
	code, err = randomPairCode()
	if err != nil {
		return "", time.Time{}, err
	}
	expires = time.Now().Add(10 * time.Minute)
	err = DB.Insert(ctx, &PairCode{Code: code, AccountName: accountName, LoginAddress: loginAddress, Expires: expires})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("saving pairing code: %v", err)
	}
	return code, expires, nil
}

// AccountChats returns Telegram chats linked to accountName.
func AccountChats(ctx context.Context, accountName string) ([]ChatInfo, error) {
	if DB == nil {
		return nil, nil
	}
	sessions, err := sessionsForAccount(ctx, accountName)
	if err != nil {
		return nil, err
	}
	out := make([]ChatInfo, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, ChatInfo{ChatID: s.ChatID, Username: s.Username, Created: s.Created})
	}
	return out, nil
}

// UnlinkChat removes a Telegram session for accountName.
func UnlinkChat(ctx context.Context, accountName string, chatID int64) error {
	s := srv
	if DB == nil {
		return ErrNotConfigured
	}
	sess, err := sessionByChat(ctx, chatID)
	if err == bstore.ErrAbsent {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if sess.AccountName != accountName {
		return ErrNotFound
	}
	if err := DB.Delete(ctx, &sess); err != nil {
		return err
	}
	if s != nil {
		s.maybeUnwatch(ctx, accountName)
		_, _ = s.client.sendMessage(ctx, chatID, "This chat was unlinked from the mailbox in the account settings.", nil, 0)
	}
	return nil
}

func (s *service) maybeUnwatch(ctx context.Context, accountName string) {
	n, err := bstore.QueryDB[Session](ctx, DB).FilterNonzero(Session{AccountName: accountName}).Count()
	if err != nil || n > 0 {
		return
	}
	s.unwatchAccount(accountName)
}
