package releasenotice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"silverwing-go/internal/storage"
)

type Store struct{ Directory string }

var sessionID = regexp.MustCompile("^[a-f0-9]{64}$")

func (s Store) path(id string) (string, error) {
	if !sessionID.MatchString(id) {
		return "", errors.New("通知标识无效")
	}
	return filepath.Join(s.Directory, id+".json"), nil
}
func (s Store) load(id string) (Session, error) {
	path, e := s.path(id)
	if e != nil {
		return Session{}, e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return Session{}, e
	}
	var v Session
	e = json.Unmarshal(b, &v)
	if e == nil && v.ID != id {
		e = errors.New("通知记录身份不匹配")
	}
	return v, e
}
func (s Store) save(v Session) error {
	path, e := s.path(v.ID)
	if e != nil {
		return e
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(s.Directory, ".notice-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return os.Rename(name, path)
}
func (s Store) Locked(id string, run func() error) error {
	if _, e := s.path(id); e != nil {
		return e
	}
	if e := os.MkdirAll(s.Directory, 0700); e != nil {
		return e
	}
	unlock, e := storage.AcquireLock(filepath.Join(s.Directory, id+".lock"))
	if e != nil {
		return e
	}
	defer unlock()
	return run()
}
func (s Store) Prepare(v Session) (Session, error) {
	if v.Audience == UserTesters {
		var out Session
		err := s.lockTag(v, func() error { var e error; out, e = s.prepare(v); return e })
		return out, err
	}
	return s.prepare(v)
}
func (s Store) prepare(v Session) (Session, error) {
	if e := v.Validate(); e != nil {
		return Session{}, e
	}
	var out Session
	err := s.Locked(v.ID, func() error {
		old, e := s.load(v.ID)
		if e == nil {
			if v.Audience == UserTesters && (old.Title != v.Title || Digest(old.Testers) != Digest(v.Testers) || Digest(old.Recipients) != Digest(v.Recipients) || old.Warning != v.Warning || old.RequirementIdentity != "" && old.RequirementIdentity != v.RequirementIdentity) {
				for _, r := range old.Receipts {
					if r.State != "sent" {
						return errors.New("待完成通知的内容或收件人快照已变化，请先核实原消息")
					}
				}
			}
			if old.Scope != v.Scope && (v.Owner == "" || old.Owner != v.Owner) {
				return errors.New("通知内容已变化，请重新发起通知")
			}
			// Metadata may refresh between openings. Keep receipts and their
			// original cards/keys, while showing the current recipient list.
			v.Receipts = old.Receipts
			out = v
			return s.save(v)
		}
		if !os.IsNotExist(e) {
			return e
		}
		if v.Audience == UserTesters {
			v.Receipts, e = s.tagHistoryReceipts(v)
			if e != nil {
				return e
			}
			out = v
			return s.save(v)
		}
		v.Receipts, e = s.compatibleReceipts(v)
		if e != nil {
			return e
		}
		out = v
		return s.save(v)
	})
	return out, err
}
func newKey() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return "uat-card-" + hex.EncodeToString(b[:]), nil
}

// Send holds the per-notice OS lock across receipts and RPC. A crash leaves
// "sending", which is uncertain; an explicit retry retains the original key.
func (s Store) Send(ctx context.Context, id string, choice Selection, g Gateway, validate func(context.Context, Session) error) (out Session, err error) {
	return s.withVerifiedSession(ctx, id, g, validate, func(out *Session) error {
		card, err := SessionCard(*out)
		if err != nil {
			return err
		}
		// Keep the original payload and key for either earlier UAT layout.
		reuseCard := false
		if receipt, ok := out.LatestReceipt(); ok && out.Kind == "" && receipt.Card != card {
			for _, applicant := range []string{"", out.Identity.Name} {
				for _, inBody := range []bool{true, false} {
					legacy, err := renderUATCard(out.Title, out.URL, applicant, out.Rows, inBody)
					if err == nil && receipt.Card == legacy {
						reuseCard = true
					}
				}
			}
		}
		_, _, err = s.sendReceipt(ctx, out, out.Identity.OpenID, card, choice, reuseCard, g)
		return err
	})
}

func (s Store) withVerifiedSession(ctx context.Context, id string, g Gateway, validate func(context.Context, Session) error, send func(*Session) error) (out Session, err error) {
	err = s.Locked(id, func() error {
		var e error
		out, e = s.load(id)
		if e != nil {
			return e
		}
		if e = out.ValidateSelf(); e != nil {
			return e
		}
		if e = validate(ctx, out); e != nil {
			return e
		}
		identity, e := g.Identity(ctx)
		if e != nil {
			return e
		}
		if e = identity.ValidateSelf(); e != nil {
			return e
		}
		if identity.AppID != out.Identity.AppID || identity.OpenID != out.Identity.OpenID {
			return errors.New("当前 Lark 账号已变化，请重新发起通知")
		}
		out.Identity = identity
		return send(&out)
	})
	return
}

type receiptRetryBlockedError string

func (e receiptRetryBlockedError) Error() string { return string(e) }

func (s Store) sendReceipt(ctx context.Context, out *Session, recipient, card string, choice Selection, reuseCard bool, g Gateway) (Receipt, bool, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, false, err
	}
	idx := -1
	for n := len(out.Receipts) - 1; n >= 0; n-- {
		if r := out.Receipts[n]; r.ChatID == "" && r.OpenID == recipient {
			idx = n
			break
		}
	}
	if choice.Resend {
		if idx < 0 || choice.ResendAfter == "" {
			return Receipt{}, false, errors.New("再次发送缺少原通知成功回执")
		}
		// Replays of the same explicit click cannot create another message.
		if out.Receipts[idx].Key != choice.ResendAfter {
			return out.Receipts[idx], true, nil
		}
		if out.Receipts[idx].State != "sent" {
			return out.Receipts[idx], false, errors.New("原通知尚未确认发送成功，请先核实或重试")
		}
	}
	if idx >= 0 && !choice.Resend {
		r := out.Receipts[idx]
		if r.State == "sent" || !choice.Retry {
			return r, true, nil
		}
		if r.Card != card && !reuseCard {
			return r, false, receiptRetryBlockedError("待重试卡片内容已变化，请先在飞书核实原消息")
		}
		if r.Card == "" || r.Key == "" {
			return r, false, receiptRetryBlockedError("原通知缺少卡片或幂等键，请先在飞书核实消息")
		}
		if age := time.Since(r.StartedAt); age < 0 || age >= time.Hour {
			return r, false, receiptRetryBlockedError("已超过一小时去重窗口，请在飞书核实消息结果")
		}
	} else {
		key, err := newKey()
		if err != nil {
			return Receipt{}, false, err
		}
		if out.Kind == Branches {
			key = strings.Replace(key, "uat-card-", "branch-card-", 1)
		}
		out.Receipts = append(out.Receipts, Receipt{OpenID: recipient, Key: key, Card: card, StartedAt: time.Now().UTC()})
		idx = len(out.Receipts) - 1
	}
	r := &out.Receipts[idx]
	r.State, r.Error = "sending", ""
	if err := s.save(*out); err != nil {
		return *r, false, err
	}
	if err := ctx.Err(); err != nil {
		return *r, false, err
	}
	answer := g.Send(ctx, recipient, r.Card, r.Key)
	r.MessageID, r.Error, r.State = answer.MessageID, answer.Error, "failed"
	if answer.MessageID != "" && answer.Error == "" {
		r.State = "sent"
	} else if answer.Unknown {
		r.State = "unknown"
	}
	return *r, false, s.save(*out)
}
