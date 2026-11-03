package networkproxy

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/http/httpproxy"
	"silverwing-go/internal/domain"
)

// NewHTTPClient takes an operation-local snapshot. Disabled targets explicitly
// connect directly, even if the parent process has proxy environment variables.
func NewHTTPClient(config domain.AppConfig, target domain.CommandProxyTarget, timeout time.Duration) (*http.Client, error) {
	transport := &http.Transport{}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	}
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 30 * time.Second
	if Enabled(config, target) {
		env, _ := Environment(config, target)
		if env["HTTPS_PROXY"] == "" {
			return nil, errors.New("请先在网络代理中配置有效的代理地址")
		}
		if _, err := domain.NormalizeCommandProxyNoProxy(config.CommandProxyNoProxy); err != nil {
			return nil, errors.New("代理例外地址无效，请检查网络代理设置")
		}
		policy := httpproxy.Config{HTTPProxy: env["HTTP_PROXY"], HTTPSProxy: env["HTTPS_PROXY"], NoProxy: env["NO_PROXY"]}
		proxy := policy.ProxyFunc()
		transport.Proxy = func(request *http.Request) (*url.URL, error) { return proxy(request.URL) }
	}
	return &http.Client{Transport: redactingTransport{Transport: transport, config: config}, Timeout: timeout}, nil
}

type redactingTransport struct {
	*http.Transport
	config domain.AppConfig
}

func (t redactingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.Transport.RoundTrip(request)
	if err != nil {
		return response, proxyError{message: Redact(t.config, err.Error()), cause: err}
	}
	return response, nil
}

type proxyError struct {
	message string
	cause   error
}

func (e proxyError) Error() string { return e.message }
func (e proxyError) Unwrap() error { return e.cause }
