// Package network provides the application's shared, immutable HTTP policies.
package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/netproxy"
	"golang.org/x/net/http/httpproxy"
)

type Mode string

const (
	System Mode = "system"
	Direct Mode = "direct"
	Custom Mode = "custom"
)

var (
	ErrInvalidConfig = errors.New("invalid network configuration")
	ErrCredentials   = errors.New("proxy credentials unavailable")
	ErrProxy         = errors.New("proxy connection or authentication failed")
	ErrNetwork       = errors.New("network request failed")
	ErrHTTPStatus    = errors.New("endpoint returned an unsuccessful HTTP status")
	ErrRedirect      = errors.New("unsafe or excessive redirect")
)

// HTTPStatusError 只保留状态码，既区分已收到响应与连接失败，也不泄漏响应正文、URL 或代理凭据。
type HTTPStatusError struct{ StatusCode int }

func (err *HTTPStatusError) Error() string {
	return fmt.Sprintf("endpoint returned HTTP %d", err.StatusCode)
}
func (err *HTTPStatusError) Unwrap() error { return ErrHTTPStatus }

// Config is safe to persist; neither a password nor a username belongs here.
type Config struct {
	Mode         Mode   `json:"mode"`
	Address      string `json:"address,omitempty"`
	CredentialID string `json:"credentialId,omitempty"`
}

type Credentials struct {
	Username string `json:"-"`
	Password string `json:"-"`
}

func (Credentials) String() string   { return "[proxy credentials redacted]" }
func (Credentials) GoString() string { return "network.Credentials{redacted}" }

type Vault interface {
	Read(context.Context, string) (Credentials, error)
	Write(context.Context, string, Credentials) error
	Delete(context.Context, string) error
}

// Client owns a policy snapshot. Redirects and streaming bodies never switch policy.
type Client struct {
	client    *http.Client
	transport *http.Transport
	allowURL  func(string) bool
}

// Manager publishes a new client only after the complete configuration validates.
type Manager struct{ current atomic.Pointer[Client] }

func New(ctx context.Context, cfg Config, vault Vault) (*Manager, error) {
	manager := &Manager{}
	if err := manager.Configure(ctx, cfg, vault); err != nil {
		return nil, err
	}
	return manager, nil
}

func Validate(cfg Config) error {
	if cfg.Mode == "" {
		cfg.Mode = System
	}
	if cfg.Mode != System && cfg.Mode != Direct && cfg.Mode != Custom {
		return ErrInvalidConfig
	}
	if cfg.Mode != Custom {
		if cfg.Address != "" || cfg.CredentialID != "" {
			return ErrInvalidConfig
		}
		return nil
	}
	_, err := proxyURL(cfg.Address)
	return err
}

func proxyURL(address string) (*url.URL, error) {
	proxy, err := url.Parse(address)
	if err != nil || proxy.User != nil || proxy.Opaque != "" || proxy.RawQuery != "" || proxy.Fragment != "" || (proxy.Path != "" && proxy.Path != "/") {
		return nil, ErrInvalidConfig
	}
	if proxy.Scheme != "http" && proxy.Scheme != "socks5" {
		return nil, ErrInvalidConfig
	}
	host, port, err := net.SplitHostPort(proxy.Host)
	if err != nil || host == "" || strings.ContainsAny(host, " \t\r\n/@\\%") {
		return nil, ErrInvalidConfig
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, ErrInvalidConfig
	}
	if net.ParseIP(host) == nil {
		if len(host) > 253 {
			return nil, ErrInvalidConfig
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, ErrInvalidConfig
			}
			for _, character := range label {
				if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-') {
					return nil, ErrInvalidConfig
				}
			}
		}
	}
	return proxy, nil
}

func (manager *Manager) Configure(ctx context.Context, cfg Config, vault Vault) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Validate(cfg); err != nil {
		return err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSHandshakeTimeout = 15 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	if cfg.Mode == System || cfg.Mode == "" {
		transport.Proxy = systemProxy
	}
	if cfg.Mode == Custom {
		proxy, _ := proxyURL(cfg.Address)
		if cfg.CredentialID != "" {
			if vault == nil {
				return ErrCredentials
			}
			secret, err := vault.Read(ctx, cfg.CredentialID)
			if err != nil {
				return ErrCredentials
			}
			if len(secret.Username) > 255 || len(secret.Password) > 255 || strings.ContainsAny(secret.Username+secret.Password, "\x00\r\n") {
				return ErrCredentials
			}
			proxy.User = url.UserPassword(secret.Username, secret.Password)
		}
		// 显式模式始终使用指定代理，包括回环地址；连接失败不得绕回直连。
		transport.Proxy = http.ProxyURL(proxy)
	}
	client := &Client{transport: transport}
	client.client = &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !safeURL(req.URL) || via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return ErrRedirect
		}
		req.Header.Del("Proxy-Authorization")
		if !strings.EqualFold(req.URL.Host, via[0].URL.Host) || req.URL.Scheme != via[0].URL.Scheme {
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
		}
		return nil
	}}
	previous := manager.current.Swap(client)
	if previous != nil {
		previous.transport.CloseIdleConnections()
	}
	return nil
}

func safeURL(address *url.URL) bool {
	return address != nil && address.User == nil && address.Host != "" && (address.Scheme == "http" || address.Scheme == "https")
}

func systemProxy(req *http.Request) (*url.URL, error) {
	host := req.URL.Hostname()
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return nil, nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil, nil
	}
	env := func(upper, lower string) string {
		if value := os.Getenv(upper); value != "" {
			return value
		}
		return os.Getenv(lower)
	}
	httpProxy, httpsProxy, allProxy, noProxy := env("HTTP_PROXY", "http_proxy"), env("HTTPS_PROXY", "https_proxy"), env("ALL_PROXY", "all_proxy"), env("NO_PROXY", "no_proxy")
	if httpProxy != "" || httpsProxy != "" || allProxy != "" || noProxy != "" {
		if httpProxy == "" {
			httpProxy = allProxy
		}
		if httpsProxy == "" {
			httpsProxy = allProxy
		}
		selected := ""
		if req.URL.Scheme == "http" {
			selected = httpProxy
		} else if req.URL.Scheme == "https" {
			selected = httpsProxy
		}
		if selected != "" {
			address := selected
			if !strings.Contains(address, "://") {
				address = "http://" + address
			}
			parsed, err := url.Parse(address)
			// httpproxy 会忽略错误代理并返回 nil，必须先校验，禁止配置错误时静默直连。
			if err != nil || parsed.Hostname() == "" || parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" && parsed.Scheme != "socks5h" {
				return nil, ErrProxy
			}
		}
		policy := httpproxy.Config{HTTPProxy: httpProxy, HTTPSProxy: httpsProxy, NoProxy: noProxy, CGI: os.Getenv("REQUEST_METHOD") != ""}
		proxy, err := policy.ProxyFunc()(req.URL)
		if err != nil {
			return nil, ErrProxy
		}
		return proxy, nil
	}
	return netproxy.Proxy(req)
}

// WithURLPolicy reuses exactly this transport while restricting every hop BEFORE
// a request is sent; isolated test feeds cannot redirect to the public Internet.
func (client *Client) WithURLPolicy(allow func(string) bool) *Client {
	copyClient := *client.client
	previous := copyClient.CheckRedirect
	copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if allow == nil || !allow(req.URL.String()) {
			return ErrRedirect
		}
		return previous(req, via)
	}
	return &Client{client: &copyClient, transport: client.transport, allowURL: allow}
}

func (manager *Manager) Snapshot() *Client { return manager.current.Load() }
func (manager *Manager) Do(req *http.Request) (*http.Response, error) {
	return manager.Snapshot().Do(req)
}

func (client *Client) Do(req *http.Request) (*http.Response, error) {
	if req == nil || !safeURL(req.URL) {
		return nil, ErrInvalidConfig
	}
	if client.allowURL != nil && !client.allowURL(req.URL.String()) {
		return nil, ErrRedirect
	}
	copy := req.Clone(req.Context())
	// 调用方误带的代理认证头不能进入源站；认证仅由 Transport 发给代理。
	copy.Header.Del("Proxy-Authorization")
	response, err := client.client.Do(copy)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		if errors.Is(err, ErrRedirect) {
			return nil, ErrRedirect
		}
		// net/http 的底层错误可能含代理 URL、用户名或口令，禁止向 UI 透传。
		return nil, ErrNetwork
	}
	if response.StatusCode == http.StatusProxyAuthRequired {
		response.Body.Close()
		return nil, ErrProxy
	}
	return response, nil
}

func (client *Client) CloseIdleConnections() { client.transport.CloseIdleConnections() }

type ConnectionResult struct {
	Endpoint   string
	StatusCode int
	Duration   time.Duration
}

// TestConnection probes the actual release endpoint, not merely the proxy port.
func (manager *Manager) TestConnection(ctx context.Context, endpoint string) (ConnectionResult, error) {
	address, err := url.Parse(endpoint)
	if err != nil || !safeURL(address) {
		return ConnectionResult{}, ErrInvalidConfig
	}
	result := ConnectionResult{Endpoint: address.Scheme + "://" + address.Host + address.EscapedPath()}
	started := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	response, err := manager.Snapshot().Do(req)
	result.Duration = time.Since(started)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, &HTTPStatusError{StatusCode: response.StatusCode}
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if err != nil {
		return result, ErrNetwork
	}
	return result, nil
}
