// Package auth stores the Beast credentials shared by the embedded clients.
package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const FileName = "credentials.json"

type Credentials struct {
	Username string `json:"-"`
	Password string `json:"-"`
}

type document struct {
	Version  int    `json:"version"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type Store struct{ Directory string }

// UserStore resolves the account file used by the standalone Genbu and Dragon tools.
func UserStore() (Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Store{}, errors.New("无法定位当前用户的共享账号目录")
	}
	return Store{Directory: filepath.Join(home, ".snowball-config")}, nil
}

func (s Store) Path() string { return filepath.Join(s.Directory, FileName) }

// Load reads only the shared account. Platform caches cannot supply credentials.
func (s Store) Load() (Credentials, error) {
	info, err := os.Lstat(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return Credentials{}, os.ErrNotExist
	}
	if err != nil || !info.Mode().IsRegular() {
		return Credentials{}, fmt.Errorf("无法读取共享账号配置")
	}
	file, err := os.Open(s.Path())
	if err != nil {
		return Credentials{}, fmt.Errorf("无法读取共享账号配置")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return Credentials{}, fmt.Errorf("共享账号配置已变化，请重试")
	}
	const maxBytes = 64 << 10
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	defer clear(data)
	if err != nil || len(data) > maxBytes {
		return Credentials{}, fmt.Errorf("无法读取共享账号配置")
	}
	return decode(data)
}

// Save is used only for an explicit account edit.
func (s Store) Save(account Credentials) (Credentials, error) {
	account.Username = strings.TrimSpace(account.Username)
	if account.Username == "" || account.Password == "" {
		return Credentials{}, fmt.Errorf("共享账号的用户名和密码不能为空")
	}
	if len(account.Username)+len(account.Password) > 8<<10 {
		return Credentials{}, fmt.Errorf("共享账号的用户名或密码过长")
	}
	if info, err := os.Lstat(s.Path()); err == nil && !info.Mode().IsRegular() {
		return Credentials{}, fmt.Errorf("共享账号配置必须是普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Credentials{}, fmt.Errorf("无法检查共享账号配置")
	}
	// An explicit save also replaces matching credentials so that older or
	// manually copied files acquire the current private permissions.
	if err := writePrivateJSON(s.Path(), document{1, account.Username, account.Password}); err != nil {
		return Credentials{}, err
	}
	return account, nil
}

// Fingerprint binds a platform session to the exact shared credentials without
// storing another plaintext password in the platform cache.
func (c Credentials) Fingerprint() string {
	hash := sha256.Sum256([]byte(c.Username + "\x00" + c.Password))
	return hex.EncodeToString(hash[:])
}

func decode(data []byte) (Credentials, error) {
	var value document
	if json.Unmarshal(trimBOM(data), &value) != nil || value.Version != 1 || strings.TrimSpace(value.Username) == "" || value.Password == "" {
		return Credentials{}, fmt.Errorf("共享账号配置无效，请在平台连接中重新保存")
	}
	return Credentials{strings.TrimSpace(value.Username), value.Password}, nil
}

func trimBOM(data []byte) []byte { return bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}) }

// writePrivateJSON atomically replaces one local authentication file.
func writePrivateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("无法创建登录配置目录")
	}
	file, err := createPrivateTemp(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("无法创建登录临时配置")
	}
	defer os.Remove(file.Name())
	fail := func() error { file.Close(); return fmt.Errorf("无法写入登录配置") }
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if encoder.Encode(value) != nil || file.Sync() != nil {
		return fail()
	}
	if file.Close() != nil {
		return fmt.Errorf("无法关闭登录临时配置")
	}
	if os.Rename(file.Name(), path) != nil {
		return fmt.Errorf("无法替换登录配置")
	}
	return nil
}
