// Package noticeprep stores recipient data until an explicit refresh replaces
// it. It never stores login authority or expires data based on elapsed time.
package noticeprep

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/releasenotice"
	"silverwing-go/internal/storage"
)

const maxBytes = 256 << 10

var ErrSuperseded = errors.New("通知准备已被需求刷新或上下文变化替代，请重新准备通知")
var ErrRefreshing = errors.New("需求通知信息正在刷新，请稍后重试")

type Scope struct {
	Config, Requirement, MeegleIdentity, Project, Kind, ID string
	MeegleContext                                          string
	Profile, AppID, Sender                                 string
}

type Snapshot struct {
	Scope          Scope                     `json:"scope"`
	FetchedAt      int64                     `json:"fetchedAt"`
	RosterComplete bool                      `json:"rosterComplete"`
	Title          string                    `json:"title"`
	Testers        []string                  `json:"testers"`
	Recipients     []releasenotice.Recipient `json:"recipients"`
	FallbackReason string                    `json:"fallbackReason,omitempty"`
	RefreshState   string                    `json:"-"`
}

type Ticket struct{ Subject, Epoch, Revision, Refresh string }
type record struct {
	Version      int       `json:"version"`
	Epoch        string    `json:"epoch"`
	Revision     string    `json:"revision"`
	DataRevision string    `json:"dataRevision,omitempty"`
	StartedAt    int64     `json:"startedAt"`
	State        string    `json:"state"`
	Snapshot     *Snapshot `json:"snapshot,omitempty"`
}

// All read/replace decisions share a short OS lock. No caller holds it during
// CLI work. A refresh fences old writers before starting remote queries.
type Store struct {
	Directory string
	now       func() time.Time
}

func (s Store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
func (s Store) locked(ctx context.Context, run func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Directory, 0700); err != nil {
		return err
	}
	for {
		release, err := storage.AcquireLock(filepath.Join(s.Directory, "preparation.lock"))
		if err == nil {
			defer release()
			return run()
		}
		if !errors.Is(err, storage.ErrLockBusy) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}
func freshID() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func (s Store) epoch(create bool) (string, error) {
	path := filepath.Join(s.Directory, "epoch")
	data, err := os.ReadFile(path)
	if err == nil && len(data) == 32 {
		return string(data), nil
	}
	if !create {
		return "", err
	}
	id, err := freshID()
	if err == nil {
		err = storage.WriteFileAtomic(path, []byte(id), 0600)
	}
	return id, err
}
func (s Store) path(subject string) string {
	return filepath.Join(s.Directory, releasenotice.Digest(subject)+".json")
}
func (s Store) read(subject string) (record, error) {
	var r record
	info, err := os.Stat(s.path(subject))
	if err != nil {
		return r, err
	}
	if info.Size() > maxBytes || info.Mode().Perm()&0200 == 0 {
		return r, errors.New("通知准备缓存不可用")
	}
	data, err := os.ReadFile(s.path(subject))
	if err == nil {
		err = json.Unmarshal(data, &r)
	}
	if err == nil && (r.Version != 1 || r.Epoch == "" || r.Revision == "") {
		err = errors.New("通知准备缓存格式不完整")
	}
	return r, err
}
func (s Store) write(subject string, r record) error {
	data, err := json.Marshal(r)
	if err == nil && len(data) > maxBytes {
		return errors.New("通知准备缓存过大")
	}
	if err != nil {
		return err
	}
	return storage.WriteFileAtomic(s.path(subject), data, 0600)
}
func (s Store) InvalidateAll(ctx context.Context) error {
	return s.locked(ctx, func() error {
		id, err := freshID()
		if err != nil {
			return err
		}
		return storage.WriteFileAtomic(filepath.Join(s.Directory, "epoch"), []byte(id), 0600)
	})
}

func (s Store) Begin(ctx context.Context, subject string, _ bool) (ticket Ticket, err error) {
	err = s.locked(ctx, func() error {
		epoch, err := s.epoch(true)
		if err != nil {
			return err
		}
		old, readErr := s.read(subject)
		id, err := freshID()
		if err != nil {
			return err
		}
		r := record{Version: 1, Epoch: epoch, Revision: id, State: "loading", StartedAt: s.clock().UnixMilli()}
		// Starting a refresh fences older writers but preserves the last usable
		// snapshot. Failure, cancellation and a crashed reader cannot erase it.
		if readErr == nil {
			r.Snapshot, r.DataRevision = old.Snapshot, old.DataRevision
		}
		if err := s.write(subject, r); err != nil {
			return err
		}
		ticket = Ticket{Subject: subject, Epoch: epoch, Revision: id}
		return nil
	})
	return
}

func (s Store) Read(ctx context.Context, subject string, scope Scope) (snapshot Snapshot, ticket Ticket, hit bool, err error) {
	snapshot, ticket, hit, err = s.Load(ctx, subject)
	if hit && snapshot.Scope != scope {
		hit = false
	}
	return
}

// Load returns data, never authority. The caller must match every account and
// configuration boundary before using it. A failed refresh retains the data.
func (s Store) Load(ctx context.Context, subject string) (snapshot Snapshot, ticket Ticket, hit bool, err error) {
	err = s.locked(ctx, func() error {
		epoch, err := s.epoch(false)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil || epoch == "" {
			return err
		}
		r, err := s.read(subject)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if r.Snapshot == nil || r.DataRevision == "" || !r.Snapshot.Valid() {
			return nil
		}
		snapshot, ticket, hit = *r.Snapshot, Ticket{Subject: subject, Revision: r.DataRevision, Refresh: r.Revision}, true
		snapshot.RefreshState = r.State
		if r.Epoch != epoch && snapshot.RefreshState == "loading" {
			snapshot.RefreshState = "cancelled"
		}
		return nil
	})
	return
}
func (p Snapshot) Valid() bool {
	if !p.RosterComplete || p.Testers == nil || p.FetchedAt <= 0 || strings.TrimSpace(p.Title) == "" || p.Scope.MeegleIdentity == "" || p.Scope.Project == "" || p.Scope.Profile == "" || p.Scope.AppID == "" || !releasenotice.ValidOpenID(p.Scope.Sender) {
		return false
	}
	if p.FallbackReason != "" {
		return len(p.Recipients) == 0
	}
	if len(p.Testers) == 0 || len(p.Recipients) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, p := range p.Recipients {
		if !releasenotice.ValidOpenID(p.OpenID) || seen[p.OpenID] {
			return false
		}
		seen[p.OpenID] = true
	}
	return true
}
func (s Store) Check(ctx context.Context, ticket Ticket) error {
	if ticket.Revision == "" {
		return nil
	}
	return s.locked(ctx, func() error {
		if ticket.Epoch != "" {
			_, err := s.current(ticket)
			return err
		}
		r, err := s.read(ticket.Subject)
		if err != nil {
			return err
		}
		if r.DataRevision != ticket.Revision || r.Revision != ticket.Refresh {
			return ErrSuperseded
		}
		epoch, err := s.epoch(false)
		if err != nil {
			return err
		}
		if r.Epoch == epoch && r.State == "loading" {
			return ErrRefreshing
		}
		return nil
	})
}
func (s Store) current(ticket Ticket) (record, error) {
	epoch, err := s.epoch(false)
	if err != nil {
		return record{}, err
	}
	r, err := s.read(ticket.Subject)
	if err != nil {
		return r, err
	}
	if epoch != ticket.Epoch || r.Epoch != ticket.Epoch || r.Revision != ticket.Revision {
		return r, ErrSuperseded
	}
	return r, nil
}
func (s Store) Commit(ctx context.Context, ticket Ticket, snapshot Snapshot) error {
	return s.locked(ctx, func() error {
		r, err := s.current(ticket)
		if err != nil {
			return err
		}
		if !snapshot.Valid() {
			return errors.New("通知准备缺少确认的完整名单")
		}
		r.State, r.Snapshot, r.DataRevision = "ready", &snapshot, ticket.Revision
		if snapshot.RefreshState == "failed" || snapshot.RefreshState == "cancelled" {
			r.State = snapshot.RefreshState
		}
		return s.write(ticket.Subject, r)
	})
}
func (s Store) Fail(ctx context.Context, ticket Ticket, state ...string) error {
	if ticket.Revision == "" {
		return nil
	}
	return s.locked(ctx, func() error {
		r, err := s.current(ticket)
		if err != nil {
			return err
		}
		r.State = "failed"
		if len(state) > 0 && state[0] == "cancelled" {
			r.State = "cancelled"
		}
		return s.write(ticket.Subject, r)
	})
}
