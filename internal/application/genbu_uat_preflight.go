package application

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"silverwing-go/internal/production"
	"silverwing-go/internal/storage"
)

type uatPodBinding struct {
	Home, ProfileID, Action, Tag string
	Target                       uatServiceTarget
	accountFingerprint           string
}

type uatPodPreflight struct {
	groupID         int
	nonce, nativeID string
}

func (a *App) uatPodBinding(request CLIUATOperationRequest, target uatServiceTarget) (uatPodBinding, error) {
	home, err := filepath.EvalSymlinks(a.Paths.Home)
	if err != nil {
		return uatPodBinding{}, err
	}
	if a.Paths.ProfileID != "" {
		id, err := storage.ReadProfileID(home)
		if err != nil || id != a.Paths.ProfileID {
			return uatPodBinding{}, errors.New("应用数据目录已变化，请重新打开操作")
		}
	}
	account, err := loadPlatformAccount()
	if err != nil {
		return uatPodBinding{}, err
	}
	return uatPodBinding{Home: home, ProfileID: a.Paths.ProfileID, Action: request.Action, Tag: request.Tag,
		Target: target, accountFingerprint: account.Fingerprint()}, nil
}

func (binding uatPodBinding) newPreflight(groupID int, nativeID string) (string, error) {
	// The nonce makes each confirmation single-use without persisting read-only
	// previews. The signed information is reproducible by another sw process.
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	data := fmt.Sprintf("%d.%x.%s", groupID, nonce, nativeID)
	return data + "." + binding.preflightSignature(data), nil
}

func (binding uatPodBinding) checkPreflight(token string) (uatPodPreflight, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || !validUATPodHex(parts[1], 32) || !validUATPodHex(parts[2], 64) || !validUATPodHex(parts[3], 64) {
		return uatPodPreflight{}, production.ErrUATPodPreflightStale
	}
	groupID, err := strconv.Atoi(parts[0])
	if err != nil || groupID < 1 || strconv.Itoa(groupID) != parts[0] {
		return uatPodPreflight{}, production.ErrUATPodPreflightStale
	}
	if !hmac.Equal([]byte(binding.preflightSignature(strings.Join(parts[:3], "."))), []byte(parts[3])) {
		return uatPodPreflight{}, production.ErrUATPodPreflightStale
	}
	return uatPodPreflight{groupID: groupID, nonce: parts[1], nativeID: parts[2]}, nil
}

func (binding uatPodBinding) preflightSignature(data string) string {
	// Every serialized field is a string or a struct of strings.
	scope, _ := json.Marshal(binding)
	m := hmac.New(sha256.New, []byte(binding.accountFingerprint))
	m.Write([]byte("silverwing-genbu-uat-preview-v1\x00"))
	m.Write(scope)
	m.Write([]byte(data))
	return hex.EncodeToString(m.Sum(nil))
}

func validUATPodHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	bytes, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(bytes) == value
}

func readUATPodReceipt(path string) (CLIUATOperationResult, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return CLIUATOperationResult{}, nil
	}
	var receipt CLIUATOperationResult
	if err != nil || json.Unmarshal(data, &receipt) != nil {
		return CLIUATOperationResult{}, errors.New("无法核对本地 UAT 操作提交记录")
	}
	switch receipt.Status {
	case "unknown":
		return receipt, production.ErrUATPodSubmitUnknown
	case "submitted", "rejected", "not-sent":
		return receipt, errors.New("此 UAT 预检编号已使用，请重新预检")
	default:
		return CLIUATOperationResult{}, errors.New("无法核对本地 UAT 操作提交记录")
	}
}

func saveUATPodReceipt(path string, receipt CLIUATOperationResult) error {
	data, err := json.Marshal(receipt)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err == nil {
		err = storage.WriteFileAtomic(path, data, 0o600)
	}
	if err != nil {
		return fmt.Errorf("保存 UAT 操作提交记录失败：%w", err)
	}
	return nil
}
