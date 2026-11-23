package genbuapi

import (
	"regexp"
	"strings"
	"unicode"
)

const redactedConfigValue = "<redacted>"

var (
	configAssignmentPattern = regexp.MustCompile(`(?i)^(\s*(?:-\s*)?(?:export\s+)?["']?)([a-z0-9_.-]+)(["']?\s*[:=]\s*)(.*)$`)
	jsonStringPairPattern   = regexp.MustCompile(`"([^"\\]+)"(\s*:\s*)("(?:\\.|[^"\\])*")`)
	privateKeyBlockPattern  = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	configJWTValuePattern   = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\b`)
	bearerPattern           = regexp.MustCompile(`(?i)(\bBearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	urlUserInfoPattern      = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)([^/\s@:]*:[^/\s@]+)@`)
	querySecretPattern      = regexp.MustCompile(`(?i)([?&;](?:user|username|password|passwd|pwd|passphrase|secret|app[_-]?secret|client[_-]?secret|token|access[_-]?key(?:id|secret)?|secret[_-]?access[_-]?key|api[_-]?key|signature|authorization)=)([^&#;\s"']+)`)
)

// redactSensitiveConfig preserves configuration structure and key names while
// masking credential values. It deliberately runs before grep so a secret value
// cannot be recovered by searching for that value.
func redactSensitiveConfig(content string, exactSecrets ...string) string {
	content = privateKeyBlockPattern.ReplaceAllString(content, redactedConfigValue)

	lines := strings.Split(content, "\n")
	redactedLines := make([]string, 0, len(lines))
	blockedIndent := -1
	for _, originalLine := range lines {
		line := strings.TrimSuffix(originalLine, "\r")
		lineEnding := ""
		if strings.HasSuffix(originalLine, "\r") {
			lineEnding = "\r"
		}

		if blockedIndent >= 0 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if leadingIndent(line) > blockedIndent {
				continue
			}
			blockedIndent = -1
		}

		redactedLine, blocksFollowingLines := redactConfigLine(line)
		redactedLines = append(redactedLines, redactedLine+lineEnding)
		if blocksFollowingLines {
			blockedIndent = leadingIndent(line)
		}
	}

	redacted := strings.Join(redactedLines, "\n")
	return redactExactSecrets(redacted, exactSecrets...)
}

func redactExactSecrets(value string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, redactedConfigValue)
		}
	}
	return value
}

func redactConfigLine(line string) (string, bool) {
	if matches := configAssignmentPattern.FindStringSubmatch(line); matches != nil && isSensitiveConfigKey(matches[2]) {
		value := strings.TrimSpace(matches[4])
		blocksFollowingLines := value == "" || strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">")
		return matches[1] + matches[2] + matches[3] + redactedAssignmentValue(value), blocksFollowingLines
	}

	line = jsonStringPairPattern.ReplaceAllStringFunc(line, func(pair string) string {
		matches := jsonStringPairPattern.FindStringSubmatch(pair)
		if matches == nil || !isSensitiveConfigKey(matches[1]) {
			return pair
		}
		return `"` + matches[1] + `"` + matches[2] + `"` + redactedConfigValue + `"`
	})
	line = urlUserInfoPattern.ReplaceAllString(line, `${1}`+redactedConfigValue+`@`)
	line = querySecretPattern.ReplaceAllString(line, `${1}`+redactedConfigValue)
	line = bearerPattern.ReplaceAllString(line, `${1}`+redactedConfigValue)
	line = configJWTValuePattern.ReplaceAllString(line, redactedConfigValue)
	return line, false
}

func redactedAssignmentValue(value string) string {
	suffix := ""
	if strings.HasSuffix(value, ",") {
		suffix = ","
		value = strings.TrimSpace(strings.TrimSuffix(value, ","))
	}
	if strings.HasPrefix(value, `"`) {
		return `"` + redactedConfigValue + `"` + suffix
	}
	if strings.HasPrefix(value, `'`) {
		return `'` + redactedConfigValue + `'` + suffix
	}
	return redactedConfigValue + suffix
}

func isSensitiveConfigKey(key string) bool {
	compact := compactConfigKey(key)
	if compact == "" {
		return false
	}

	for _, fragment := range []string{
		"password", "passwd", "passphrase", "pwd",
		"secret", "privatekey", "accesskey", "apikey", "appkey",
		"signkey", "signaturekey", "encryptkey", "decryptkey", "hmackey",
		"keypass", "credential", "authorization", "authtoken",
		"refreshtoken", "accesstoken", "idtoken", "sessionid",
		"cookie", "username", "jwt", "dsn",
	} {
		if strings.Contains(compact, fragment) {
			return true
		}
	}

	if strings.Contains(compact, "token") &&
		!strings.HasSuffix(compact, "tokenurl") &&
		!strings.HasSuffix(compact, "tokenuri") &&
		!strings.HasSuffix(compact, "tokenpath") &&
		!strings.HasSuffix(compact, "tokenendpoint") {
		return true
	}

	lastPart := compactConfigKey(lastConfigKeyPart(key))
	if lastPart == "auth" || lastPart == "account" ||
		strings.HasSuffix(lastPart, "mailaccount") || strings.HasSuffix(lastPart, "bankaccount") ||
		strings.HasSuffix(lastPart, "email") || strings.HasSuffix(lastPart, "phone") ||
		strings.HasSuffix(lastPart, "mobile") || strings.HasSuffix(lastPart, "idcard") ||
		strings.HasSuffix(lastPart, "cardno") || strings.HasSuffix(lastPart, "recipient") ||
		strings.HasSuffix(lastPart, "recipients") {
		return true
	}

	return strings.HasSuffix(compact, "key") && !strings.Contains(compact, "publickey")
}

func compactConfigKey(value string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func lastConfigKeyPart(key string) string {
	if index := strings.LastIndexAny(key, ".-_"); index >= 0 {
		return key[index+1:]
	}
	return key
}

func leadingIndent(line string) int {
	indent := 0
	for _, character := range line {
		switch character {
		case ' ':
			indent++
		case '\t':
			indent += 4
		default:
			return indent
		}
	}
	return indent
}

// RedactSensitiveConfig masks sensitive values while preserving structure.
func RedactSensitiveConfig(content string, exactSecrets ...string) string {
	return redactSensitiveConfig(content, exactSecrets...)
}

// RedactExactSecrets masks exact secret values from an arbitrary string.
func RedactExactSecrets(value string, secrets ...string) string {
	return redactExactSecrets(value, secrets...)
}

// RedactValues applies token-pattern and exact-secret masking to logs/errors.
func RedactValues(value string, secrets ...string) string {
	return redactValues(value, secrets...)
}

// Redact masks token-like material in API errors and logs.
func Redact(value string) string { return redact(value) }

// IsSensitiveConfigKey reports whether a key name identifies sensitive data.
func IsSensitiveConfigKey(key string) bool { return isSensitiveConfigKey(key) }

// FilterConfigLines returns matching lines after callers have applied
// RedactSensitiveConfig. It is the shared case-insensitive config filter.
func FilterConfigLines(content, needle string) []string {
	if strings.TrimSpace(needle) == "" {
		return nil
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lowerNeedle := strings.ToLower(needle)
	lines := strings.Split(normalized, "\n")
	matches := make([]string, 0)
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if strings.Contains(strings.ToLower(line), lowerNeedle) {
			matches = append(matches, line)
		}
	}
	return matches
}
