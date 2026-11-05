package releasenotice

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Earlier receipts were keyed by the full configuration, including contacts.
// Copy matching history into the current ledger; never alter the old files.
// Legacy UAT ownership is proven by the complete globally-unique operation IDs.
// Branch notifications require the explicit task owner, since branch names
// alone may repeat in different tasks.
func (s Store) compatibleReceipts(v Session) ([]Receipt, error) {
	if len(v.Rows) == 0 {
		return nil, nil
	}
	if v.Owner == "" && v.Kind != Branches {
		for _, r := range v.Rows {
			if r.OperationID == "" {
				return nil, nil
			}
		}
	}
	entries, e := os.ReadDir(s.Directory)
	if e != nil {
		return nil, e
	}
	byKey := map[string]Receipt{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		old, e := s.load(strings.TrimSuffix(entry.Name(), ".json"))
		if e != nil || old.ID == v.ID {
			continue
		}
		owned := v.Owner != "" && old.Owner == v.Owner
		if !owned && v.Kind == "" && old.Owner == "" {
			owned = true
			for _, r := range v.Rows {
				if r.OperationID == "" {
					owned = false
				}
			}
		}
		if !owned || old.Kind != v.Kind || old.URL != v.URL || old.Identity.AppID != v.Identity.AppID ||
			old.Identity.OpenID != v.Identity.OpenID || Digest(old.Rows) != Digest(v.Rows) {
			continue
		}
		for _, r := range old.Receipts {
			if r.Key == "" || r.ChatID != "" || r.OpenID != v.Identity.OpenID {
				continue
			}
			current, exists := byKey[r.Key]
			if !exists || receiptCertainty(r) > receiptCertainty(current) {
				byKey[r.Key] = r
			}
		}
	}
	out := make([]Receipt, 0, len(byKey))
	for _, r := range byKey {
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].Key < out[j].Key
		}
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out, nil
}
func receiptCertainty(r Receipt) int {
	if r.State == "sent" && r.MessageID != "" {
		return 3
	}
	if r.State == "unknown" || r.State == "sending" {
		return 2
	}
	return 1
}
