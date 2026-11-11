package releasenotice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const UserTesters = "user-testers-v1"

type Recipient struct {
	Name   string `json:"name"`
	OpenID string `json:"openId"`
}
type UserGateway interface {
	Gateway
	SendingIdentity() string
}

// Refresh checks the complete prepared snapshot and returns the online-verified
// sender. BeforeSend checks current account/profile, task, config and batch after
// asynchronous work. Both are mandatory; a local check cannot authorize a send.
type TagSendChecks struct {
	Refresh    func(context.Context, Session) (Identity, error)
	BeforeSend func(context.Context, Session) error
	now        func() time.Time
}

// Only the first pending recipient may reuse the check immediately preceding
// reservation. Evidence never survives this SendTag call or any message RPC.
const tagValidationMaxAge = 5 * time.Second

func (i Identity) ValidateUser() error {
	if i.AppID == "" || !ValidOpenID(i.OpenID) || !i.UserVerified {
		return errors.New("当前 Lark 用户未通过登录验证，请使用当前 profile 登录后重试")
	}
	return nil
}

func (s Session) Validate() error {
	if s.Audience != UserTesters {
		return s.ValidateSelf()
	}
	if s.Kind != "" || s.Sender != "user" || s.Profile == "" || s.Owner == "" || len(s.People) != 0 || len(s.Recipients) == 0 {
		return errors.New("用户通知策略无效，请重新准备通知")
	}
	if err := s.Identity.ValidateUser(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range s.Recipients {
		if !ValidOpenID(p.OpenID) || seen[p.OpenID] {
			return errors.New("通知收件人无效或重复")
		}
		seen[p.OpenID] = true
	}
	if s.ID != s.tagID() {
		return errors.New("通知收件人或身份已变化，请重新准备通知")
	}
	return nil
}

func (s *Session) BindTagOwner(owner, profile string) {
	s.Owner, s.Profile, s.Audience, s.Sender = owner, profile, UserTesters, "user"
	s.ID = s.tagID()
}
func (s Session) tagID() string {
	ids := []string{}
	for _, p := range s.Recipients {
		ids = append(ids, p.OpenID)
	}
	sort.Strings(ids)
	return Digest([]any{UserTesters, s.Owner, s.Profile, s.Identity.AppID, s.Identity.OpenID, s.URL, s.Rows, ids})
}
func (s Session) recipientReceipt(id string) (Receipt, bool) {
	for n := len(s.Receipts) - 1; n >= 0; n-- {
		r := s.Receipts[n]
		if r.ChatID == "" && r.OpenID == id {
			return r, true
		}
	}
	return Receipt{}, false
}
func (s Session) receiptSet() string {
	keys := []string{}
	for _, p := range s.Recipients {
		r, _ := s.recipientReceipt(p.OpenID)
		keys = append(keys, r.Key)
	}
	return Digest(keys)
}
func (s Session) tagSendChoice() (Selection, error) {
	choice := s.RetryChoice()
	if err := s.Validate(); err != nil {
		return choice, err
	}
	all := true
	for _, p := range s.Recipients {
		r, ok := s.recipientReceipt(p.OpenID)
		if !ok || r.State != "sent" || r.MessageID == "" {
			all = false
		}
		if ok && r.State == "sent" && r.Key == "" {
			return choice, errors.New("成功回执缺少幂等键，请先核实消息")
		}
	}
	if all {
		choice.Resend = true
		choice.ResendAfter = s.receiptSet()
	}
	return choice, nil
}

// The preparation snapshot is separate from stable receipt identity: metadata
// edits invalidate an outstanding click without duplicating successful delivery.
func (s Session) RetryChoice() Selection {
	values := []any{s.Scope, s.Profile, s.Identity.AppID, s.Identity.OpenID, s.Title, s.URL, s.Rows, canonicalTesters(s.Testers), s.Recipients, s.Warning}
	// Keep legacy snapshots byte-compatible until a newly prepared request
	// explicitly binds its Meegle account. Receipt identity and cards are stable.
	if s.RequirementIdentity != "" {
		values = append(values, s.RequirementIdentity)
	}
	return Selection{Retry: true, Snapshot: Digest(values)}
}

// Empty tester lists are omitted by Session's JSON encoding and load as nil.
// Use the persisted representation in digests without changing nonempty rosters.
func canonicalTesters(testers []string) []string {
	if len(testers) == 0 {
		return nil
	}
	return testers
}

func (s Session) Deliveries() []RecipientDelivery {
	out := []RecipientDelivery{}
	for _, p := range s.Recipients {
		r, ok := s.recipientReceipt(p.OpenID)
		if !ok {
			r.State = "pending"
		}
		out = append(out, RecipientDelivery{Name: p.Name, Self: p.OpenID == s.Identity.OpenID, State: r.State, MessageID: r.MessageID, Error: r.Error})
	}
	return out
}
func (s Session) DeliverySummary() string {
	parts := []string{}
	for _, d := range s.Deliveries() {
		state := "待发送"
		switch d.State {
		case "sent":
			state = "已发送"
		case "failed":
			state = "失败"
		case "unknown", "sending":
			state = "待核实"
		}
		name := d.Name
		if name == "" {
			name = "收件人"
		}
		parts = append(parts, name+"："+state)
	}
	if s.Warning != "" {
		parts = append(parts, s.Warning)
	}
	if s.PreparationStatus != "" {
		parts = append(parts, s.PreparationStatus)
	}
	return strings.Join(parts, "；")
}

// Do not turn an unresolved send into a fresh send merely by changing recipients.
// Bot sessions and different sender accounts are deliberately separate histories.
func (s Store) tagHistoryReceipts(v Session) ([]Receipt, error) {
	files, err := os.ReadDir(s.Directory)
	if err != nil {
		return nil, err
	}
	latest := map[string]Receipt{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".json")
		if !sessionID.MatchString(id) || id == v.ID {
			continue
		}
		old, err := s.load(id)
		if err != nil {
			return nil, fmt.Errorf("读取既有通知回执失败：%w", err)
		}
		if old.Audience != UserTesters || old.Owner != v.Owner || old.Profile != v.Profile || old.Identity.AppID != v.Identity.AppID || old.Identity.OpenID != v.Identity.OpenID || old.URL != v.URL || Digest(old.Rows) != Digest(v.Rows) {
			continue
		}
		for _, r := range old.Receipts {
			if r.State == "unknown" || r.State == "sending" || r.State == "queued" {
				return nil, errors.New("原收件人通知仍有待核实结果，不能变更接收人后重新发送")
			}
			if r.State == "sent" && r.ChatID == "" && r.MessageID != "" && r.Key != "" {
				if prev, ok := latest[r.OpenID]; !ok || r.StartedAt.After(prev.StartedAt) {
					latest[r.OpenID] = r
				}
			}
		}
	}
	out := []Receipt{}
	for _, p := range v.Recipients {
		if r, ok := latest[p.OpenID]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// Serialize recipient changes and sends for the same task/batch, across processes.
func (s Store) lockTag(v Session, run func() error) error {
	return s.Locked(Digest([]any{"tag-send-lock", v.Owner, v.Profile, v.Identity.AppID, v.Identity.OpenID, v.URL, v.Rows}), run)
}

// SendTag reserves the whole attempt before RPC, so crashes and stale repeated
// clicks cannot resend successful recipients or skip unstarted recipients.
func (s Store) SendTag(ctx context.Context, id string, choice Selection, g UserGateway, checks TagSendChecks) (out Session, err error) {
	snapshot, err := s.load(id)
	if err != nil {
		return out, err
	}
	err = s.lockTag(snapshot, func() error { var e error; out, e = s.sendTag(ctx, id, choice, g, checks); return e })
	return
}
func (s Store) sendTag(ctx context.Context, id string, choice Selection, g UserGateway, checks TagSendChecks) (out Session, err error) {
	err = s.Locked(id, func() error {
		var e error
		out, e = s.load(id)
		if e != nil {
			return e
		}
		if out.Audience != UserTesters || g.SendingIdentity() != "user" {
			return errors.New("Tag 通知必须以当前用户身份重新准备发送")
		}
		if e = out.Validate(); e != nil {
			return e
		}
		if choice.Snapshot == "" || choice.Snapshot != out.RetryChoice().Snapshot {
			return errors.New("通知准备快照已变化，请重新准备通知")
		}
		if checks.Refresh == nil || checks.BeforeSend == nil {
			return errors.New("Tag 通知缺少发送前校验")
		}
		history, e := s.tagHistoryReceipts(out)
		if e != nil {
			return e
		}
		for _, r := range history {
			if _, exists := out.recipientReceipt(r.OpenID); !exists {
				out.Receipts = append(out.Receipts, r)
			}
		}
		now := checks.now
		if now == nil {
			now = time.Now
		}
		var verifiedAt time.Time
		refresh := func() error {
			if e := ctx.Err(); e != nil {
				return e
			}
			identity, e := checks.Refresh(ctx, out)
			if e != nil {
				return e
			}
			if e = identity.ValidateUser(); e != nil {
				return e
			}
			if identity.AppID != out.Identity.AppID || identity.OpenID != out.Identity.OpenID {
				return errors.New("当前 Lark 账号已变化，请重新准备通知")
			}
			verifiedAt = now()
			return ctx.Err()
		}
		beforeSend := func() error {
			if e := ctx.Err(); e != nil {
				return e
			}
			if e := checks.BeforeSend(ctx, out); e != nil {
				return e
			}
			if age := now().Sub(verifiedAt); age < 0 || age >= tagValidationMaxAge {
				return errors.New("发送前校验已过期，请重新发起通知")
			}
			return ctx.Err()
		}
		if e = refresh(); e != nil {
			return e
		}
		if choice.Resend {
			if choice.ResendAfter == "" {
				return errors.New("再次发送缺少原回执标识")
			}
			if choice.ResendAfter != out.receiptSet() {
				return errors.New("发送请求已处理，请刷新通知结果")
			}
			current, e := out.tagSendChoice()
			if e != nil {
				return e
			}
			if !current.Resend {
				return errors.New("原通知尚未全部成功，请重试未完成项")
			}
		}
		card, e := SessionCard(out)
		if e != nil {
			return e
		}
		for _, p := range out.Recipients {
			if _, ok := out.recipientReceipt(p.OpenID); ok && !choice.Resend {
				continue
			}
			key, e := newKey()
			if e != nil {
				return e
			}
			out.Receipts = append(out.Receipts, Receipt{OpenID: p.OpenID, Name: p.Name, Key: key, Card: card, State: "queued", StartedAt: time.Now().UTC()})
		}
		if e = s.save(out); e != nil {
			return e
		}
		checked := false
		for _, p := range out.Recipients {
			previous, _ := out.recipientReceipt(p.OpenID)
			if previous.State == "sent" {
				continue
			}
			if checked {
				if e = refresh(); e != nil {
					return e
				}
			}
			if e = beforeSend(); e != nil {
				return e
			}
			checked = true
			_, _, e = s.sendReceipt(ctx, &out, p.OpenID, card, Selection{Retry: true}, true, g)
			if e != nil {
				return e
			}
		}
		if !checked {
			// CLI repeats still validate the current account, even when all
			// successful receipts mean there is no RPC to perform.
			if e = beforeSend(); e != nil {
				return e
			}
		}
		for _, d := range out.Deliveries() {
			if d.State != "sent" {
				return errors.New("通知未全部发送成功：" + out.DeliverySummary())
			}
		}
		return nil
	})
	return
}
