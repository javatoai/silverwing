package tools

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBase64AlphabetsPaddingAndWhitespace(t *testing.T) {
	const input = "??>??? "
	tests := []struct {
		name, encoded   string
		urlSafe, padded bool
	}{
		{"standard", "Pz8+Pz8/IA==", false, true},
		{"raw standard", "Pz8+Pz8/IA", false, false},
		{"URL-safe", "Pz8-Pz8_IA==", true, true},
		{"raw URL-safe", "Pz8-Pz8_IA", true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, err := EncodeBase64(input, test.urlSafe, test.padded); err != nil || got != test.encoded {
				t.Fatalf("encoding = %q, %v", got, err)
			}
			wrapped := " \t" + test.encoded[:4] + "\r\n" + test.encoded[4:] + "\v\f"
			if got, err := DecodeBase64(wrapped); err != nil || got != input {
				t.Fatalf("decoding = %q, %v", got, err)
			}
		})
	}
	for _, input := range []string{"", " \r\n中文🙂\t "} {
		encoded, err := EncodeBase64(input, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := DecodeBase64(encoded); err != nil || got != input {
			t.Fatalf("round trip = %q, %v", got, err)
		}
	}
}

func TestBase64RejectsInvalidTextAndEnforcesLimits(t *testing.T) {
	for _, invalid := range []string{"AA=A", "Zg===", "Zg=", "ZE==", "Pz8-Pz8/", "Z", "Zg==\u00a0"} {
		if got, err := DecodeBase64(invalid); err == nil || got != "" {
			t.Errorf("invalid Base64 %q returned %q, %v", invalid, got, err)
		}
	}
	for _, binary := range []string{"\xff", "one\x00two"} {
		if _, err := EncodeBase64(binary, false, true); err == nil || !strings.Contains(err.Error(), "UTF-8") {
			t.Errorf("encode binary error = %v", err)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(binary))
		if _, err := DecodeBase64(encoded); err == nil || !strings.Contains(err.Error(), "UTF-8") {
			t.Errorf("decode binary error = %v", err)
		}
	}
	limit := strings.Repeat("a", MaxTextBytes)
	encoded, err := EncodeBase64(limit, false, true)
	if err != nil {
		t.Fatal("encode text at limit", err)
	}
	if got, err := DecodeBase64(encoded); err != nil || got != limit {
		t.Fatalf("decode text at limit: length = %d, error = %v", len(got), err)
	}
	if _, err := EncodeBase64(limit+"a", false, true); err == nil {
		t.Fatal("encoded oversized text")
	}
	if _, err := DecodeBase64(base64.StdEncoding.EncodeToString([]byte(limit + "a"))); err == nil {
		t.Fatal("decoded oversized text")
	}
	if _, err := DecodeBase64(strings.Repeat(" ", MaxBase64InputBytes+1)); err == nil {
		t.Fatal("accepted oversized input containing ignored whitespace")
	}
}
