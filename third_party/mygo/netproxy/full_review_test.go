//go:build !js

package netproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const fullReviewProxyCaseEnvironment = "MYGO_FULL_REVIEW_PROXY_CASE"

type fullReviewProxyRequest struct {
	URL, Want string
}

type fullReviewProxyCase struct {
	Name            string
	Environment     map[string]string
	Requests        []fullReviewProxyRequest
	WantSystemCalls int
}

// Each child starts with a genuinely fresh default envProxy. In particular,
// this does not replace http.ProxyFromEnvironment with a stub or depend on
// the process-global environment cache left by another test.
func TestProxyFullReviewEnvironmentResolver(t *testing.T) {
	if encoded := os.Getenv(fullReviewProxyCaseEnvironment); encoded != "" {
		var scenario fullReviewProxyCase
		if err := json.Unmarshal([]byte(encoded), &scenario); err != nil {
			t.Fatal(err)
		}
		fullReviewCheckRealProxy(t, scenario)
		return
	}
	const fallback = "socks5://fallback.invalid:1080"
	scenarios := []fullReviewProxyCase{
		{Name: "uppercase_all_socks5", Environment: map[string]string{"ALL_PROXY": fallback}, Requests: []fullReviewProxyRequest{
			{"http://public.invalid/", fallback}, {"https://public.invalid/", fallback},
		}},
		{Name: "lowercase_all_socks5h", Environment: map[string]string{"all_proxy": "socks5h://lower.invalid:1081"}, Requests: []fullReviewProxyRequest{
			{"http://public.invalid/", "socks5h://lower.invalid:1081"}, {"https://public.invalid/", "socks5h://lower.invalid:1081"},
		}},
		{Name: "uppercase_all_http", Environment: map[string]string{"ALL_PROXY": "http://fallback.invalid:8080"}, Requests: []fullReviewProxyRequest{
			{"http://public.invalid/", "http://fallback.invalid:8080"}, {"https://public.invalid/", "http://fallback.invalid:8080"},
		}},
		{Name: "lowercase_all_https", Environment: map[string]string{"all_proxy": "https://fallback.invalid:8443"}, Requests: []fullReviewProxyRequest{
			{"http://public.invalid/", "https://fallback.invalid:8443"}, {"https://public.invalid/", "https://fallback.invalid:8443"},
		}},
		{Name: "http_specific_over_all", Environment: map[string]string{"ALL_PROXY": fallback, "HTTP_PROXY": "http://http-only.invalid:3128"}, Requests: []fullReviewProxyRequest{
			{"http://public.invalid/", "http://http-only.invalid:3128"}, {"https://public.invalid/", fallback},
		}},
		{Name: "https_specific_over_all", Environment: map[string]string{"ALL_PROXY": fallback, "HTTPS_PROXY": "http://https-only.invalid:3129"}, Requests: []fullReviewProxyRequest{
			{"http://public.invalid/", fallback}, {"https://public.invalid/", "http://https-only.invalid:3129"},
		}},
		{Name: "both_uppercase_specific_over_all", Environment: map[string]string{"ALL_PROXY": fallback, "HTTP_PROXY": "http://http.invalid:3128", "HTTPS_PROXY": "http://https.invalid:3129"}, Requests: []fullReviewProxyRequest{
			{"http://public.invalid/", "http://http.invalid:3128"}, {"https://public.invalid/", "http://https.invalid:3129"},
		}},
		{Name: "both_lowercase_specific_over_all", Environment: map[string]string{"all_proxy": fallback, "http_proxy": "http://lower-http.invalid:3128", "https_proxy": "http://lower-https.invalid:3129"}, Requests: []fullReviewProxyRequest{
			{"http://public.invalid/", "http://lower-http.invalid:3128"}, {"https://public.invalid/", "http://lower-https.invalid:3129"},
		}},
		{Name: "uppercase_no_proxy_domain_cidr_port", Environment: map[string]string{"ALL_PROXY": fallback, "NO_PROXY": "example.invalid,.private.invalid,10.0.0.0/8,api.special.invalid:8443"}, Requests: []fullReviewProxyRequest{
			{"https://example.invalid/", ""}, {"https://sub.example.invalid/", ""}, {"https://sub.private.invalid/", ""},
			{"https://private.invalid/", fallback}, {"http://10.23.4.5/", ""}, {"http://11.23.4.5/", fallback},
			{"https://api.special.invalid:8443/", ""}, {"https://api.special.invalid/", fallback}, {"https://public.invalid/", fallback},
		}},
		{Name: "lowercase_no_proxy_wildcard", Environment: map[string]string{"all_proxy": fallback, "no_proxy": "*"}, Requests: []fullReviewProxyRequest{
			{"http://public.invalid/", ""}, {"https://another.invalid/", ""},
		}},
		{Name: "no_proxy_also_bypasses_specific_proxy", Environment: map[string]string{"ALL_PROXY": fallback, "HTTP_PROXY": "http://specific.invalid:3128", "HTTPS_PROXY": "http://specific.invalid:3129", "NO_PROXY": "direct.invalid"}, Requests: []fullReviewProxyRequest{
			{"http://direct.invalid/", ""}, {"https://direct.invalid/", ""}, {"http://public.invalid/", "http://specific.invalid:3128"}, {"https://public.invalid/", "http://specific.invalid:3129"},
		}},
		{Name: "loopback_is_direct_with_all", Environment: map[string]string{"ALL_PROXY": fallback}, Requests: []fullReviewProxyRequest{
			{"http://localhost:5173/", ""}, {"https://LocalHost/", ""}, {"https://app.localhost/", ""},
			{"http://127.0.0.1:8080/", ""}, {"https://127.4.5.6/", ""}, {"http://[::1]:3000/", ""},
		}},
		{Name: "no_proxy_only_is_an_explicit_environment_choice", Environment: map[string]string{"NO_PROXY": "direct.invalid"}, Requests: []fullReviewProxyRequest{
			{"https://direct.invalid/", ""}, {"https://public.invalid/", ""},
		}},
		{Name: "empty_environment_uses_fake_system", Environment: map[string]string{}, Requests: []fullReviewProxyRequest{
			{"https://system-check.invalid/", "http://system.invalid:3128"},
		}, WantSystemCalls: 1},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			encoded, err := json.Marshal(scenario)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProxyFullReviewEnvironmentResolver$", "-test.count=1")
			command.Env = fullReviewProxyChildEnvironment(scenario.Environment)
			command.Env = append(command.Env, fullReviewProxyCaseEnvironment+"="+string(encoded))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("fresh real-resolver process failed: %v (context=%v)\n%s", err, ctx.Err(), output)
			}
		})
	}
}

func fullReviewCheckRealProxy(t *testing.T, scenario fullReviewProxyCase) {
	t.Helper()
	// Only the system resolver is faked: an environment miss must not read
	// the machine's configuration or perform PAC/WPAD/network activity.
	asked, _ := fakeSystem(t, "http://system.invalid:3128")
	for _, request := range scenario.Requests {
		req, err := http.NewRequest(http.MethodGet, request.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		proxy, err := Proxy(req)
		if err != nil {
			t.Errorf("%s: Proxy(%s): %v", scenario.Name, request.URL, err)
			continue
		}
		if got := urlString(proxy); got != request.Want {
			t.Errorf("%s: Proxy(%s)=%q, want %q", scenario.Name, request.URL, got, request.Want)
		}
	}
	if *asked != scenario.WantSystemCalls {
		t.Errorf("%s: system resolver called %d times, want %d", scenario.Name, *asked, scenario.WantSystemCalls)
	}
}

func fullReviewProxyChildEnvironment(values map[string]string) []string {
	var clean []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		remove := strings.EqualFold(name, fullReviewProxyCaseEnvironment) || strings.EqualFold(name, "REQUEST_METHOD")
		for _, proxyName := range envNames {
			remove = remove || strings.EqualFold(name, proxyName)
		}
		if !remove {
			clean = append(clean, entry)
		}
	}
	for name, value := range values {
		clean = append(clean, name+"="+value)
	}
	return clean
}
