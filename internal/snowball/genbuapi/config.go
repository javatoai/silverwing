package genbuapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBeastURL          = beastBaseURL
	DefaultGenbuURL          = genbuBaseURL
	DefaultTimeout           = requestTimeout
	SystemCode               = systemCode
	LoginPath                = loginPath
	DragonUserPath           = dragonUserPath
	ApplicationsPath         = applicationsPath
	VersionPath              = versionPath
	BaseInfoPath             = baseInfoPath
	BuildLogPath             = buildLogPath
	AppEnvNamespacePath      = appEnvNamespacePath
	ConfigMapListPath        = configMapListPath
	PodGroupPath             = podGroupPath
	PodPath                  = podPath
	UATRedeployPath          = uatRedeployPath
	UATReleaseGroupsPath     = uatReleaseGroupsPath
	UATDeployInfoPath        = uatDeployInfoPath
	UATReleasePath           = uatReleasePath
	UATReleaseEnablePath     = uatReleaseEnablePath
	BuildStepNumber          = buildStepNumber
	UATStepNumber            = uatStepNumber
	ProductionStepNumber     = productionStepNumber
	GenbuTimeLayout          = genbuTimeLayout
	DefaultBuildLogTailLines = 100
	MinBuildLogTailLines     = 1
	MaxBuildLogTailLines     = 1000
	tokenExpirySkew          = 60 * time.Second
)

// Config is the complete explicit configuration needed to call Genbu. URLs,
// credentials, and tokens must be supplied by the caller.
type Config struct {
	BeastURL       string        `json:"-"`
	GenbuURL       string        `json:"-"`
	Username       string        `json:"-"`
	Password       string        `json:"-"`
	Token          string        `json:"-"`
	TokenExpiresAt int64         `json:"-"`
	Timeout        time.Duration `json:"-"`
	HTTPClient     *http.Client  `json:"-"`
}

// Session contains the refreshed Genbu token for a caller that needs to
// persist it. Callers should keep it out of user-visible output.
type Session struct {
	Token          string `json:"-"`
	TokenExpiresAt int64  `json:"-"`
}

type Client struct {
	mu         sync.Mutex
	config     Config
	httpClient *http.Client
	generated  uint64
	now        func() time.Time
}

// NewClient creates an in-memory, synchronized Genbu client. It performs no
// implicit file or home-directory lookup.
func NewClient(config Config) (*Client, error) {
	if strings.TrimSpace(config.BeastURL) == "" || strings.TrimSpace(config.GenbuURL) == "" {
		return nil, fmt.Errorf("Genbu BeastURL and GenbuURL must be configured")
	}
	if config.Timeout <= 0 {
		return nil, fmt.Errorf("Genbu timeout must be greater than zero")
	}
	for _, endpoint := range []string{config.BeastURL, config.GenbuURL} {
		parsed, err := url.ParseRequestURI(endpoint)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("Genbu endpoint URL is invalid")
		}
	}
	if config.TokenExpiresAt < 0 {
		return nil, fmt.Errorf("Genbu token expiry must not be negative")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: config.Timeout}
	}
	return &Client{config: config, httpClient: httpClient, now: time.Now}, nil
}

// ConfigSnapshot returns a synchronized copy of the client's current config.
// It includes credentials and must be treated as secret material.
func (client *Client) ConfigSnapshot() Config {
	client.mu.Lock()
	defer client.mu.Unlock()
	config := client.config
	config.HTTPClient = nil
	return config
}

// TokenExpiration extracts exp from the JWT payload or protected header.
func TokenExpiration(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	for _, segment := range []string{parts[1], parts[0]} {
		decoded, err := base64.RawURLEncoding.DecodeString(segment)
		if err != nil {
			continue
		}
		var claims map[string]any
		if err := json.Unmarshal(decoded, &claims); err != nil {
			continue
		}
		if expiration, ok := claims["exp"].(float64); ok {
			return int64(expiration)
		}
	}
	return 0
}

// TokenIsUsable applies the CLI's existing one-minute expiration safety skew.
func TokenIsUsable(token string, expiresAt int64, now time.Time) bool {
	if token == "" || expiresAt <= 0 {
		return false
	}
	return time.Unix(expiresAt, 0).After(now.Add(tokenExpirySkew))
}

func (client *Client) worker(ctx context.Context) *genbuClient {
	return &genbuClient{
		httpClient: client.httpClient,
		beastURL:   client.config.BeastURL,
		genbuURL:   client.config.GenbuURL,
		ctx:        ctx,
	}
}

func (client *Client) tokenFor(ctx context.Context) (string, uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if TokenIsUsable(client.config.Token, client.config.TokenExpiresAt, client.now()) {
		return client.config.Token, client.generated, nil
	}
	if strings.TrimSpace(client.config.Username) == "" || client.config.Password == "" {
		if client.config.Token != "" {
			return client.config.Token, client.generated, nil
		}
		return "", client.generated, &authenticationError{message: "Genbu credentials or token must be configured"}
	}
	token, err := client.worker(ctx).login(client.config.Username, client.config.Password)
	if err != nil {
		return "", client.generated, err
	}
	client.config.Token = token
	client.config.TokenExpiresAt = TokenExpiration(token)
	client.generated++
	return client.config.Token, client.generated, nil
}

func (client *Client) refresh(ctx context.Context, observedGeneration uint64) (string, uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.generated != observedGeneration && client.config.Token != "" {
		return client.config.Token, client.generated, nil
	}
	if strings.TrimSpace(client.config.Username) == "" || client.config.Password == "" {
		return "", client.generated, &authenticationError{message: "Genbu token was rejected and credentials are unavailable for refresh"}
	}
	token, err := client.worker(ctx).login(client.config.Username, client.config.Password)
	if err != nil {
		return "", client.generated, err
	}
	client.config.Token = token
	client.config.TokenExpiresAt = TokenExpiration(token)
	client.generated++
	return client.config.Token, client.generated, nil
}

func (client *Client) requestToken(ctx context.Context, run func(*genbuClient, string) error) error {
	ctx, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()
	token, generation, err := client.tokenFor(ctx)
	if err != nil {
		return err
	}
	err = run(client.worker(ctx), token)
	if !IsAuthenticationError(err) {
		return err
	}
	token, generation, err = client.refresh(ctx, generation)
	if err != nil {
		return err
	}
	err = run(client.worker(ctx), token)
	_ = generation
	return err
}
