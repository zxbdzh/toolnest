package lab

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSignedFixtureRejectsDifferentTrustKey(t *testing.T) {
	archive, err := Archive([]byte("MZ-synthetic"), "0.1.1")
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:]))
	environment, err := StartSigned(archive, signature, base64.StdEncoding.EncodeToString(public))
	if err != nil {
		t.Fatal(err)
	}
	environment.Close()
	environment.Close()
	public[0] ^= 1
	if environment, err = StartSigned(archive, signature, base64.StdEncoding.EncodeToString(public)); err == nil {
		environment.Close()
		t.Fatal("untrusted fixture accepted")
	}
}

func TestAuthenticatedProxyCannotBecomeGeneralForwarder(t *testing.T) {
	environment, err := Start([]byte("MZ-synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	address, err := url.Parse(environment.HTTPProxy)
	if err != nil {
		t.Fatal(err)
	}
	address.User = url.UserPassword(environment.Username, environment.Password)
	transport := &http.Transport{Proxy: http.ProxyURL(address)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:1/not-our-service", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected target allowed: %d", response.StatusCode)
	}
}

func TestProxyStatsTrackAuthenticatedTrafficWithoutSecrets(t *testing.T) {
	for _, protocol := range []string{"http", "socks5"} {
		t.Run(protocol, func(t *testing.T) {
			environment, err := Start([]byte("MZ-synthetic"))
			if err != nil {
				t.Fatal(err)
			}
			defer environment.Close()
			address := environment.HTTPProxy
			if protocol == "socks5" {
				address = environment.SOCKSProxy
			}
			proxyURL, err := url.Parse(address)
			if err != nil {
				t.Fatal(err)
			}
			proxyURL.User = url.UserPassword(environment.Username, environment.Password)
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			response, err := client.Get(environment.Feed + "/health")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("authenticated request failed: %d", response.StatusCode)
			}
			if protocol == "socks5" {
				// 真实客户端会复用 SOCKS5 隧道，第二个 HTTP 请求不应被误算成第二次代理认证。
				response, err = client.Get(environment.Feed + "/health")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					t.Fatalf("reused tunnel request failed: %d", response.StatusCode)
				}
			}
			direct := &http.Transport{Proxy: nil}
			defer direct.CloseIdleConnections()
			observer := &http.Client{Transport: direct, Timeout: 3 * time.Second}
			readStats := func(leaked bool) {
				t.Helper()
				response, err := observer.Get(environment.Feed + "/stats")
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{environment.Username, environment.Password, "Authorization"} {
					if strings.Contains(string(body), secret) {
						t.Fatal("statistics disclosed authentication material")
					}
				}
				var stats struct {
					HTTPRequests      int64 `json:"httpRequests"`
					SOCKSRequests     int64 `json:"socksRequests"`
					LeakedCredentials bool  `json:"leakedCredentials"`
				}
				if err := json.Unmarshal(body, &stats); err != nil {
					t.Fatal(err)
				}
				if stats.LeakedCredentials != leaked {
					t.Fatal("credential leakage detector did not match observed origin traffic")
				}
				if protocol == "http" && (stats.HTTPRequests != 1 || stats.SOCKSRequests != 0) || protocol == "socks5" && (stats.SOCKSRequests != 1 || stats.HTTPRequests != 0) {
					t.Fatal("statistics did not identify the actual authenticated proxy")
				}
			}
			readStats(false)
			// 用刻意直送源站的认证头验证泄漏探针确实能变红，避免仅凭默认 false 通过验收。
			request, err := http.NewRequest(http.MethodGet, environment.Feed+"/health", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.SetBasicAuth(environment.Username, environment.Password)
			response, err = observer.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			readStats(true)
		})
	}
}
