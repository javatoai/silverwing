package production

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/storage"
)

var batchIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func newID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

type Store struct {
	Directory   string
	writeAtomic func(string, []byte, os.FileMode) error
	removeFile  func(string) error
}

func (s Store) Load(id string) (Batch, error) {
	if !batchIDPattern.MatchString(id) {
		return Batch{}, errors.New("生产批次标识无效")
	}
	path := filepath.Join(s.Directory, id+".json")
	info, err := os.Lstat(path)
	if err != nil {
		return Batch{}, err
	}
	if !info.Mode().IsRegular() {
		return Batch{}, errors.New("生产批次记录不是普通文件")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Batch{}, err
	}
	var b Batch
	if err = json.Unmarshal(data, &b); err != nil {
		return b, fmt.Errorf("生产批次记录损坏：%w", err)
	}
	if b.ID != id || b.Schema != 1 {
		return b, errors.New("生产批次记录身份或版本不匹配")
	}
	unversioned := b.Version == "" && b.ReleaseBranch == "" && b.InitialSHA == "" && b.ReleaseSHA == "" && len(b.Branches) == 0 && len(b.Tags) == 0 && b.TagPlan == nil
	if err := ValidateVersion(b.Version, nil); !unversioned && (err != nil || b.ReleaseBranch != "release/"+b.Version) {
		return b, errors.New("生产批次版本与 release 分支不匹配")
	}
	return b, nil
}

func (s Store) List() ([]Batch, error) {
	entries, err := os.ReadDir(s.Directory)
	if os.IsNotExist(err) {
		return []Batch{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]Batch, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		b, err := s.Load(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt > result[j].CreatedAt })
	return result, nil
}

// Callers hold the repository lock across intent, Git mutation and result save.
func (s Store) Save(b *Batch) error {
	if !batchIDPattern.MatchString(b.ID) {
		return errors.New("生产批次标识无效")
	}
	if err := os.MkdirAll(s.Directory, 0o700); err != nil {
		return err
	}
	b.Schema, b.UpdatedAt = 1, timestamp()
	if b.Intent != nil {
		b.Intent.Preview.ProductionStatus = normalizeStatus(b.Intent.Preview.ProductionStatus)
	}
	if b.TagPlan != nil {
		b.TagPlan.Preview.ProductionStatus = normalizeStatus(b.TagPlan.Preview.ProductionStatus)
	}
	for i := range b.Tags {
		b.Tags[i].Status = normalizeStatus(b.Tags[i].Status)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	write := s.writeAtomic
	if write == nil {
		write = storage.WriteFileAtomic
	}
	return write(filepath.Join(s.Directory, b.ID+".json"), data, 0o600)
}

func (s Store) remove(id string) error {
	if !batchIDPattern.MatchString(id) {
		return errors.New("生产批次标识无效")
	}
	path := filepath.Join(s.Directory, id+".json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("生产批次记录不是普通文件")
	}
	remove := s.removeFile
	if remove == nil {
		remove = os.Remove
	}
	if err := remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除本地批次记录失败：%w", err)
	}
	return nil
}
