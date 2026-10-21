package production

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
)

// BatchRecordToken binds a confirmation to all fields of the loaded record.
// It is transient confirmation data, never part of the on-disk format.
func BatchRecordToken(b Batch) (string, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	token := sha256.Sum256(data)
	return hex.EncodeToString(token[:]), nil
}

// Delete removes only an ended batch's local JSON record. In particular it
// never resolves a repository, contacts a remote, or cleans retained worktrees.
func (s *Service) Delete(ctx context.Context, id, expectedToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if token, err := hex.DecodeString(expectedToken); err != nil || len(token) != sha256.Size {
		return errors.New("请重新打开批次并确认删除")
	}
	b, err := s.Store.Load(id)
	if os.IsNotExist(err) {
		return nil // Retrying an already completed local deletion is harmless.
	}
	if err != nil {
		return err
	}
	return s.locks.WithRepository(b.Target.CommonDirectory, func() error {
		current, err := s.Store.Load(id)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !current.Ended {
			return errors.New("只能删除已结束的生产批次")
		}
		token, err := BatchRecordToken(current)
		if err != nil {
			return err
		}
		if token != expectedToken {
			return errors.New("批次记录已变化，请重新打开后确认删除")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.Store.remove(id)
	})
}
