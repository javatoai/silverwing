package tools

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"
)

const MaxBase64InputBytes = 3 << 20

// EncodeBase64 encodes UTF-8 text, including its original whitespace.
func EncodeBase64(input string, urlSafe, padded bool) (string, error) {
	if err := checkSize(input); err != nil {
		return "", err
	}
	if !utf8.ValidString(input) || strings.IndexByte(input, 0) >= 0 {
		return "", errors.New("请输入不含空字符（NUL）的 UTF-8 文本")
	}
	encoding := base64.StdEncoding
	if urlSafe {
		encoding = base64.URLEncoding
	}
	if !padded {
		encoding = encoding.WithPadding(base64.NoPadding)
	}
	return encoding.EncodeToString([]byte(input)), nil
}

// DecodeBase64 recognizes both Base64 alphabets and optional padding.
func DecodeBase64(input string) (string, error) {
	if len(input) > MaxBase64InputBytes {
		return "", errors.New("Base64 输入不能超过 3 MiB")
	}
	compact := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			return -1
		default:
			return r
		}
	}, input)
	encoding := base64.StdEncoding
	if strings.ContainsAny(compact, "-_") {
		encoding = base64.URLEncoding
	}
	if !strings.HasSuffix(compact, "=") {
		encoding = encoding.WithPadding(base64.NoPadding)
	}
	decoded, err := encoding.Strict().DecodeString(compact)
	if err != nil {
		return "", errors.New("无效的 Base64，请检查字符和末尾填充")
	}
	if len(decoded) > MaxTextBytes {
		return "", errors.New("解码后的文本不能超过 2 MiB")
	}
	if !utf8.Valid(decoded) || bytes.IndexByte(decoded, 0) >= 0 {
		return "", errors.New("解码结果无法显示为 UTF-8 文本，可能包含二进制数据或空字符（NUL）")
	}
	return string(decoded), nil
}
