package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const profileIdentityFile = "profile.json"

type profileIdentity struct {
	Schema int    `json:"schema"`
	ID     string `json:"id"`
}

// CheckProfileDirectory reads the configuration format before an application
// creates any files in an uninitialized or older home.
func CheckProfileDirectory(home string) error {
	store := NewConfigStore(NewPaths(home))
	if !store.hasConfigurationData() {
		return nil
	}
	config, err := store.readCurrent()
	if err != nil {
		return err
	}
	return store.validateProfileConfigPaths(config)
}

func ReadProfileID(home string) (string, error) {
	path := filepath.Join(home, profileIdentityFile)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || manifestPathReparse(info) {
		return "", errors.New("实例标识必须是普通文件")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var identity profileIdentity
	if json.Unmarshal(data, &identity) != nil || identity.Schema != 1 || !validProfileID(identity.ID) {
		return "", errors.New("应用数据目录的实例标识无效")
	}
	return identity.ID, nil
}

func validProfileID(id string) bool {
	raw, err := hex.DecodeString(id)
	return err == nil && len(raw) == 16 && hex.EncodeToString(raw) == id
}

func LoadOrCreateProfileID(home string) (string, error) {
	if id, err := ReadProfileID(home); !errors.Is(err, os.ErrNotExist) {
		return id, err
	}
	if err := CheckProfileDirectory(home); err != nil {
		return "", err
	}
	lock := filepath.Join(home, "locks", "profile-init.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		release, err := AcquireLock(lock)
		if err == nil {
			defer release()
			break
		}
		if !errors.Is(err, ErrLockBusy) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("应用数据目录正在初始化：%w", err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	if id, err := ReadProfileID(home); !errors.Is(err, os.ErrNotExist) {
		return id, err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	data, err := json.Marshal(profileIdentity{Schema: 1, ID: id})
	if err != nil {
		return "", err
	}
	if err := WriteFileAtomic(filepath.Join(home, profileIdentityFile), append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	return id, nil
}
