package dragonapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBeastURL  = "https://beast.snowballtech.com"
	DefaultDragonURL = "https://dragon.snowballtech.com"
	DefaultTimeout   = 20 * time.Second
	loginPath        = "/beast/api/login"
	exchangePath     = "/dragon/api/admin/user/beastUser"
	approvalPath     = "/dragon/api/approval/approval"
	templatePath     = "/dragon/api/approval/process/structure/24"
	maxResponseBytes = 8 << 20
)

type Config struct {
	BeastURL       string                      `json:"-"`
	DragonURL      string                      `json:"-"`
	Username       string                      `json:"-"`
	Password       string                      `json:"-"`
	Token          string                      `json:"-"`
	TokenExpiresAt int64                       `json:"-"`
	UserID         int64                       `json:"-"`
	Timeout        time.Duration               `json:"-"`
	HTTPClient     *http.Client                `json:"-"`
	CheckAdmission func(context.Context) error `json:"-"`
}

type Session struct {
	Token          string `json:"-"`
	TokenExpiresAt int64  `json:"-"`
	UserID         int64  `json:"-"`
}

// Client uses explicit configuration; it never reads browser or home-directory data.
type Client struct {
	config     Config
	httpClient *http.Client
	mu         sync.Mutex
	session    Session
}

type apiError struct {
	status         int
	code           string
	message        string
	authentication bool
}

// requestStartError indicates that the protected request did not reach the transport.
type requestStartError struct{ cause error }

func (e *requestStartError) Error() string { return e.cause.Error() }
func (e *requestStartError) Unwrap() error { return e.cause }

// A transport can report that it blocked a request before sending it.
type unsentRequest interface {
	error
	RequestNotSent()
}

func (e *apiError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("Dragon 接口失败（%s）：%s", e.code, e.message)
	}
	return fmt.Sprintf("接口 HTTP %d：%s", e.status, e.message)
}

func NewClient(config Config) (*Client, error) {
	if config.Timeout <= 0 {
		return nil, fmt.Errorf("请求超时必须大于零")
	}
	for _, raw := range []string{config.BeastURL, config.DragonURL} {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("接口地址配置无效")
		}
	}
	config.BeastURL = strings.TrimRight(config.BeastURL, "/")
	config.DragonURL = strings.TrimRight(config.DragonURL, "/")
	if config.TokenExpiresAt < 0 || config.UserID < 0 {
		return nil, fmt.Errorf("登录配置无效")
	}
	hc := &http.Client{Timeout: config.Timeout}
	if config.HTTPClient != nil {
		copy := *config.HTTPClient
		hc = &copy
	}
	// Never forward internal authentication headers to a redirect destination.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{config: config, httpClient: hc, session: Session{config.Token, config.TokenExpiresAt, config.UserID}}, nil
}

func (c *Client) SessionSnapshot() Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

// TokenExpiration supports the Beast/Dragon JWT exp field in payload or header.
func TokenExpiration(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	for _, part := range []string{parts[1], parts[0]} {
		b, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			continue
		}
		var claims struct {
			Exp int64 `json:"exp"`
		}
		if json.Unmarshal(b, &claims) == nil && claims.Exp > 0 {
			return claims.Exp
		}
	}
	return 0
}

func (c *Client) Login(ctx context.Context) (Session, error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked(ctx)
}

func (c *Client) loginLocked(ctx context.Context) (Session, error) {
	if strings.TrimSpace(c.config.Username) == "" || c.config.Password == "" {
		return Session{}, fmt.Errorf("请先配置 Genbu / Dragon 共享账号")
	}
	data, headers, err := c.request(ctx, http.MethodPost, c.config.BeastURL+loginPath,
		map[string]string{"username": c.config.Username, "password": c.config.Password}, Session{}, c.config.Password, c.session.Token)
	if err != nil {
		return Session{}, err
	}
	beastToken := extractToken(data)
	if beastToken == "" {
		beastToken = headers.Get("X-Token")
	}
	if beastToken == "" {
		return Session{}, fmt.Errorf("Beast 登录响应缺少 token")
	}
	endpoint := c.config.DragonURL + exchangePath + "?" + url.Values{"token": {beastToken}}.Encode()
	data, headers, err = c.request(ctx, http.MethodGet, endpoint, nil, Session{}, c.config.Password, beastToken, c.session.Token)
	if err != nil {
		return Session{}, err
	}
	var body struct {
		User struct {
			UserID int64 `json:"userId"`
		} `json:"user"`
	}
	if json.Unmarshal(data, &body) != nil {
		return Session{}, fmt.Errorf("Dragon 登录响应格式错误")
	}
	token := extractToken(data)
	if token == "" {
		token = headers.Get("X-Token")
	}
	if token == "" || body.User.UserID <= 0 {
		return Session{}, fmt.Errorf("Dragon 登录响应缺少 token 或用户编号")
	}
	c.session = Session{token, TokenExpiration(token), body.User.UserID}
	return c.session, nil
}

func (c *Client) ensureSession(ctx context.Context) (Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.session
	if s.Token != "" && s.UserID > 0 && time.Unix(s.TokenExpiresAt, 0).After(time.Now().Add(time.Minute)) {
		return s, nil
	}
	return c.loginLocked(ctx)
}

func (c *Client) authenticated(ctx context.Context, method, path string, payload any) (json.RawMessage, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, &requestStartError{err}
	}
	data, headers, err := c.request(ctx, method, c.config.DragonURL+path, payload, session, c.config.Password, session.Token)
	var auth *apiError
	// Only read requests may be repeated after authentication refresh.
	if method == http.MethodGet && errors.As(err, &auth) && auth.authentication {
		session, err = c.Login(ctx)
		if err != nil {
			return nil, err
		}
		data, headers, err = c.request(ctx, method, c.config.DragonURL+path, payload, session, c.config.Password, session.Token)
	}
	if err == nil && headers.Get("new-token") != "" {
		c.mu.Lock()
		if c.session.Token == session.Token {
			c.session.Token = headers.Get("new-token")
			c.session.TokenExpiresAt = TokenExpiration(c.session.Token)
		}
		c.mu.Unlock()
	}
	return data, err
}

func (c *Client) request(ctx context.Context, method, endpoint string, payload any, session Session, secrets ...string) (json.RawMessage, http.Header, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, &requestStartError{errors.New("无法编码接口请求")}
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, nil, &requestStartError{errors.New("无法构造接口请求")}
	}
	if method == http.MethodPost {
		req.GetBody = nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if session.Token != "" {
		req.Header.Set("x-token", session.Token)
		req.Header.Set("x-user-id", strconv.FormatInt(session.UserID, 10))
	}
	if c.config.CheckAdmission != nil {
		if err := c.config.CheckAdmission(ctx); err != nil {
			return nil, nil, &requestStartError{err}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, &requestStartError{err}
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		var stopped unsentRequest
		if errors.As(err, &stopped) {
			return nil, nil, &requestStartError{stopped}
		}
		return nil, nil, fmt.Errorf("接口连接失败或请求超时")
	}
	defer res.Body.Close()
	content, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("读取接口响应失败")
	}
	if len(content) > maxResponseBytes {
		return nil, nil, fmt.Errorf("接口响应超过大小限制")
	}
	var envelope struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	decoded := json.Unmarshal(content, &envelope) == nil
	if !decoded {
		envelope.Code, envelope.Data = "", nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || (decoded && envelope.Code != "" && envelope.Code != "000000") {
		secrets = append(secrets, res.Header.Get("x-token"), res.Header.Get("new-token"))
		message := "请求未成功"
		if decoded && envelope.Message != "" {
			message = redactErrorField(envelope.Message, 500, secrets...)
		}
		return envelope.Data, nil, &apiError{
			status: res.StatusCode, code: redactErrorField(envelope.Code, 64, secrets...), message: message,
			authentication: res.StatusCode == http.StatusUnauthorized || envelope.Code == "000401",
		}
	}
	if !decoded || envelope.Code != "000000" || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return envelope.Data, nil, fmt.Errorf("接口响应格式错误或缺少 data")
	}
	return envelope.Data, res.Header, nil
}

func extractToken(data json.RawMessage) string {
	var v map[string]json.RawMessage
	if json.Unmarshal(data, &v) != nil {
		return ""
	}
	for _, name := range []string{"token", "access_token", "accessToken"} {
		var token string
		if json.Unmarshal(v[name], &token) == nil && token != "" {
			return token
		}
	}
	for _, name := range []string{"response", "data", "result"} {
		if token := extractToken(v[name]); token != "" {
			return token
		}
	}
	return ""
}

var jwtPattern = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)

// Redact removes known authentication material from user-facing errors/output.
func Redact(text string, secrets ...string) string {
	ordered := slices.Clone(secrets)
	slices.SortFunc(ordered, func(a, b string) int { return len(b) - len(a) })
	replacements := make([]string, 0, len(ordered)*2)
	for _, secret := range ordered {
		if secret != "" {
			replacements = append(replacements, secret, "<redacted>")
		}
	}
	text = strings.NewReplacer(replacements...).Replace(text)
	return jwtPattern.ReplaceAllString(text, "<redacted>")
}

func redactErrorField(raw string, limit int, secrets ...string) string {
	variants := make([]string, 0, len(secrets)*3)
	for _, secret := range secrets {
		variants = append(variants, secret, url.QueryEscape(secret), url.PathEscape(secret))
	}
	value := []rune(Redact(raw, variants...))
	return string(value[:min(len(value), limit)])
}
