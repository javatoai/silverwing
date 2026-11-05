// Package releasenotice owns self-notification cards and durable receipts.
package releasenotice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

type Identity struct {
	AppID     string `json:"appId"`
	Name      string `json:"name"`
	OpenID    string `json:"openId"`
	BotReady  bool   `json:"botReady"`
	UserState string `json:"userState,omitempty"`
}

// Person only decodes retired recipient lists so stale sessions can be rejected.
type Person struct {
	OpenID string `json:"openId"`
	ChatID string `json:"chatId,omitempty"`
}
type Row struct {
	OperationID string `json:"operationId"`
	Service     string `json:"service"`
	Version     string `json:"version"`
	Branch      string `json:"branch,omitempty"`
}
type Receipt struct {
	OpenID    string    `json:"openId"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	Card      string    `json:"card"`
	State     string    `json:"state"`
	MessageID string    `json:"messageId,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	ChatID    string    `json:"chatId,omitempty"`
}
type Session struct {
	ID       string    `json:"id"`
	Scope    string    `json:"scope"`
	Identity Identity  `json:"identity"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Rows     []Row     `json:"rows"`
	People   []Person  `json:"people,omitempty"`
	Receipts []Receipt `json:"receipts"`
	Kind     string    `json:"kind,omitempty"`
	Audience string    `json:"audience,omitempty"`
	Owner    string    `json:"owner,omitempty"`
}

// A request's configuration scope may change while its task/content is the
// same. Keep the receipt identity stable across those confirmed re-openings.
func (s *Session) BindOwner(owner string) {
	s.Owner, s.Audience = owner, SelfOnly
	s.ID = Digest([]any{SelfOnly, owner, s.Kind, s.Identity.AppID, s.Identity.OpenID, s.URL, s.Rows})
}

type Selection struct {
	Retry       bool   `json:"retry"`
	Resend      bool   `json:"resend"`
	ResendAfter string `json:"resendAfter,omitempty"`
}
type SendResult struct {
	MessageID string
	Error     string
	Unknown   bool
}
type Gateway interface {
	Identity(context.Context) (Identity, error)
	Send(context.Context, string, string, string) SendResult
}

const Branches = "branches"

const SelfOnly = "self-v1"

var openIDPattern = regexp.MustCompile("^ou_[A-Za-z0-9]+$")

func ValidOpenID(id string) bool { return openIDPattern.MatchString(id) }
func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ValidateSelf deliberately does not require a display name or a fresh user token.
func (i Identity) ValidateSelf() error {
	if i.AppID == "" || !i.BotReady {
		return errors.New("Lark 通知机器人不可用，请检查应用配置")
	}
	if !ValidOpenID(i.OpenID) {
		return errors.New("未取得当前 Lark 账号的有效 open_id，请检查本地 CLI 身份")
	}
	return nil
}

func (s Session) ValidateSelf() error {
	if s.Audience != SelfOnly || len(s.People) != 0 {
		return errors.New("通知接收策略已变化，请重新发起仅本人通知")
	}
	return s.Identity.ValidateSelf()
}

func (s Session) LatestReceipt() (Receipt, bool) {
	for n := len(s.Receipts) - 1; n >= 0; n-- {
		r := s.Receipts[n]
		if r.ChatID == "" && r.OpenID == s.Identity.OpenID {
			return r, true
		}
	}
	return Receipt{}, false
}

// A fresh explicit click may resend a success; uncertain attempts keep their key.
func (s Session) SendChoice() (Selection, error) {
	choice := Selection{Retry: true}
	if e := s.ValidateSelf(); e != nil {
		return choice, e
	}
	if r, ok := s.LatestReceipt(); ok && r.State == "sent" {
		if r.Key == "" {
			return choice, errors.New("原通知回执缺少幂等键，请先核实消息")
		}
		choice.Resend, choice.ResendAfter = true, r.Key
	}
	return choice, nil
}
