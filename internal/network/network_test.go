package network

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryVault struct{ secret Credentials }

func (vault memoryVault) Read(context.Context, string) (Credentials, error) { return vault.secret, nil }
func (memoryVault) Write(context.Context, string, Credentials) error        { return nil }
func (memoryVault) Delete(context.Context, string) error                    { return nil }

func newManager(t *testing.T, cfg Config, vault Vault) *Manager {
	t.Helper()
	manager, err := New(context.Background(), cfg, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Snapshot().CloseIdleConnections() })
	return manager
}
func request(t *testing.T, manager *Manager, endpoint string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	response, err := manager.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestValidate(t *testing.T) {
	for _, address := range []string{"http://localhost:3128", "socks5://127.0.0.1:1080", "http://[::1]:8080"} {
		if err := Validate(Config{Mode: Custom, Address: address}); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{"ftp://host:3", "https://host:3", "http://host", "http://host:0", "http://host:65536", "http://user:secret@host:80", "http://host:80/path", "http://bad_host:80", "http://-bad:80", "http://host:80?secret=x", "socks4://host:2", "http://host:80/#x"} {
		if Validate(Config{Mode: Custom, Address: address}) == nil {
			t.Errorf("accepted %q", address)
		}
	}
	if Validate(Config{Mode: Mode("unknown")}) == nil || Validate(Config{Mode: Direct, Address: "http://host:80"}) == nil {
		t.Fatal("invalid policy accepted")
	}
}

func TestDirectIgnoresEnvironmentAndCustomDoesNotFallback(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	var originCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { originCalls.Add(1); fmt.Fprint(writer, "origin") }))
	defer origin.Close()
	manager := newManager(t, Config{Mode: Direct}, nil)
	if request(t, manager, origin.URL) != "origin" {
		t.Fatal("direct request failed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	manager = newManager(t, Config{Mode: Custom, Address: "http://" + address}, nil)
	req, _ := http.NewRequest(http.MethodGet, origin.URL, nil)
	if _, err = manager.Do(req); err == nil {
		t.Fatal("unavailable proxy bypassed")
	}
	if originCalls.Load() != 1 {
		t.Fatal("custom policy silently reached origin")
	}
}

func TestAuthenticatedHTTPProxyRedirectNoCredentialLeak(t *testing.T) {
	var targetCalls, proxyCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		targetCalls.Add(1)
		if req.Header.Get("Proxy-Authorization") != "" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Error("credentials leaked to redirected origin")
		}
		fmt.Fprint(writer, "final")
	}))
	defer origin.Close()
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("test-user:secret-pass"))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		proxyCalls.Add(1)
		if req.Header.Get("Proxy-Authorization") != expected {
			writer.WriteHeader(407)
			return
		}
		if req.URL.Path == "/redirect" {
			http.Redirect(writer, req, origin.URL, 302)
			return
		}
		copy := req.Clone(req.Context())
		copy.RequestURI = ""
		copy.Header.Del("Proxy-Authorization")
		response, err := transport.RoundTrip(copy)
		if err != nil {
			writer.WriteHeader(502)
			return
		}
		defer response.Body.Close()
		writer.WriteHeader(response.StatusCode)
		_, _ = io.Copy(writer, response.Body)
	}))
	defer proxy.Close()
	manager := newManager(t, Config{Mode: Custom, Address: proxy.URL, CredentialID: "test"}, memoryVault{Credentials{"test-user", "secret-pass"}})
	req, _ := http.NewRequest(http.MethodGet, "http://release.invalid/redirect", nil)
	req.Header.Set("Proxy-Authorization", "malicious-secret")
	req.Header.Set("Authorization", "origin-token")
	req.Header.Set("Cookie", "sensitive=1")
	response, err := manager.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if string(data) != "final" || proxyCalls.Load() != 2 || targetCalls.Load() != 1 {
		t.Fatal("redirect did not keep shared proxy policy")
	}
	bad := newManager(t, Config{Mode: Custom, Address: proxy.URL, CredentialID: "bad"}, memoryVault{Credentials{"wrong", "do-not-log"}})
	req, _ = http.NewRequest(http.MethodGet, "http://release.invalid/", nil)
	_, err = bad.Do(req)
	if !errors.Is(err, ErrProxy) || strings.Contains(err.Error(), "do-not-log") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("unsafe authentication error: %v", err)
	}
}

func TestCancellationSnapshotAndInvalidReconfigure(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		close(started)
		<-req.Context().Done()
		close(cancelled)
	}))
	defer origin.Close()
	manager := newManager(t, Config{Mode: Direct}, nil)
	snapshot := manager.Snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, origin.URL, nil)
	finished := make(chan error, 1)
	go func() { _, err := snapshot.Do(req); finished <- err }()
	<-started
	if err := manager.Configure(context.Background(), Config{Mode: Custom, Address: "http://127.0.0.1:1"}, nil); err != nil {
		t.Fatal(err)
	}
	if manager.Snapshot() == snapshot {
		t.Fatal("configuration not atomically replaced")
	}
	current := manager.Snapshot()
	if manager.Configure(context.Background(), Config{Mode: Custom, Address: "not a proxy"}, nil) == nil || manager.Snapshot() != current {
		t.Fatal("invalid configuration displaced current policy")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not propagate")
	}
	<-cancelled
}

func TestSystemEnvironmentPriority(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { calls.Add(1); fmt.Fprint(writer, "environment") }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	if runtime.GOOS != "windows" {
		// Windows 环境变量大小写不敏感，不能制造两个不同值来测试 Unix 优先级。
		t.Setenv("http_proxy", "http://127.0.0.1:1")
	}
	manager := newManager(t, Config{Mode: System}, nil)
	if request(t, manager, "http://not-a-real-host.invalid/test") != "environment" {
		t.Fatal("uppercase env did not win")
	}
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("ALL_PROXY", proxy.URL)
	if request(t, manager, "http://not-a-real-host.invalid/all") != "environment" {
		t.Fatal("ALL_PROXY was ignored")
	}
	t.Setenv("NO_PROXY", "not-a-real-host.invalid")
	req, _ := http.NewRequest(http.MethodGet, "http://not-a-real-host.invalid/", nil)
	selected, err := systemProxy(req)
	if err != nil || selected != nil {
		t.Fatal("NO_PROXY ignored")
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected environment routes")
	}
}

func TestSOCKS5Authentication(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Proxy-Authorization") != "" {
			t.Error("SOCKS secret leaked")
		}
		fmt.Fprint(writer, "socks-ok")
	}))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	errorsCh := make(chan error, 2)
	var workers sync.WaitGroup
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			errorsCh <- err
			return
		}
		workers.Add(1)
		defer workers.Done()
		defer connection.Close()
		errorsCh <- serveSOCKS(connection, "user", "pass")
	}()
	manager := newManager(t, Config{Mode: Custom, Address: "socks5://" + listener.Addr().String(), CredentialID: "socks"}, memoryVault{Credentials{"user", "pass"}})
	if request(t, manager, origin.URL) != "socks-ok" {
		t.Fatal("SOCKS request failed")
	}
	manager.Snapshot().CloseIdleConnections()
	select {
	case err := <-errorsCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SOCKS session hung")
	}
	workers.Wait()
}

func serveSOCKS(connection net.Conn, username, password string) error {
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(connection)
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	if _, err := connection.Write([]byte{5, 2}); err != nil {
		return err
	}
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	user := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, user); err != nil {
		return err
	}
	length, err := reader.ReadByte()
	if err != nil {
		return err
	}
	pass := make([]byte, int(length))
	if _, err = io.ReadFull(reader, pass); err != nil {
		return err
	}
	if string(user) != username || string(pass) != password {
		_, _ = connection.Write([]byte{1, 1})
		return errors.New("incorrect SOCKS authentication")
	}
	_, _ = connection.Write([]byte{1, 0})
	connect := make([]byte, 4)
	if _, err = io.ReadFull(reader, connect); err != nil {
		return err
	}
	var host string
	switch connect[3] {
	case 1:
		address := make([]byte, 4)
		if _, err = io.ReadFull(reader, address); err != nil {
			return err
		}
		host = net.IP(address).String()
	case 3:
		length, err = reader.ReadByte()
		if err != nil {
			return err
		}
		address := make([]byte, int(length))
		if _, err = io.ReadFull(reader, address); err != nil {
			return err
		}
		host = string(address)
	case 4:
		address := make([]byte, 16)
		if _, err = io.ReadFull(reader, address); err != nil {
			return err
		}
		host = net.IP(address).String()
	default:
		return errors.New("unexpected SOCKS address")
	}
	port := make([]byte, 2)
	if _, err = io.ReadFull(reader, port); err != nil {
		return err
	}
	upstream, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(int(port[0])*256+int(port[1]))), time.Second)
	if err != nil {
		return err
	}
	defer upstream.Close()
	_, _ = connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, reader)
		if tcp, ok := upstream.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		close(done)
	}()
	_, _ = io.Copy(connection, upstream)
	_ = connection.Close()
	<-done
	return nil
}

func TestURLPolicyRejectsRedirectBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { calls.Add(1); fmt.Fprint(writer, "unexpected") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) { http.Redirect(writer, req, target.URL, 302) }))
	defer redirect.Close()
	manager := newManager(t, Config{Mode: Direct}, nil)
	policy := manager.Snapshot().WithURLPolicy(func(raw string) bool {
		parsed, _ := url.Parse(raw)
		return parsed.Host == strings.TrimPrefix(redirect.URL, "http://")
	})
	req, _ := http.NewRequest(http.MethodGet, redirect.URL, nil)
	if _, err := policy.Do(req); !errors.Is(err, ErrRedirect) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("disallowed redirect was already requested")
	}
}

func TestConnectionChecksActualEndpoint(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusServiceUnavailable) }))
	defer origin.Close()
	manager := newManager(t, Config{Mode: Direct}, nil)
	result, err := manager.TestConnection(context.Background(), origin.URL+"/release?not-in-display=secret")
	if err == nil || result.StatusCode != 503 || strings.Contains(result.Endpoint, "secret") {
		t.Fatalf("wrong diagnostic: %+v %v", result, err)
	}
}

func TestConnectionHTTPResponseIsNotTransportFailure(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(status)
				fmt.Fprint(writer, "private-response-must-not-be-reported")
			}))
			defer origin.Close()
			manager := newManager(t, Config{Mode: Direct}, nil)
			result, err := manager.TestConnection(context.Background(), origin.URL+"/releases/latest?token=private-query")
			if err == nil || result.StatusCode != status || !errors.Is(err, ErrHTTPStatus) || errors.Is(err, ErrNetwork) || !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Fatalf("HTTP response was misreported as a transport failure: status=%d result=%+v err=%v", status, result, err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(result.Endpoint, "private") {
				t.Fatal("connection diagnostics disclosed response body or query")
			}
		})
	}
}

func TestOptionalLiveProxyConnectionDiagnostics(t *testing.T) {
	address := os.Getenv("TOOLNEST_TEST_LIVE_PROXY")
	if address == "" {
		t.Skip("explicit live loopback proxy not supplied; ordinary tests never contact GitHub")
	}
	proxy, err := proxyURL(address)
	if err != nil || net.ParseIP(proxy.Hostname()) == nil || !net.ParseIP(proxy.Hostname()).IsLoopback() {
		t.Fatal("live diagnostics require a valid numeric loopback proxy")
	}
	manager := newManager(t, Config{Mode: Custom, Address: address}, nil)
	for _, endpoint := range []struct {
		name, url string
		status    int
	}{
		{"github-api", "https://api.github.com/", http.StatusOK},
		{"placeholder-update", "https://api.github.com/repos/toolnest-test/toolnest/releases/latest", http.StatusNotFound},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			result, err := manager.TestConnection(ctx, endpoint.url)
			if result.StatusCode != endpoint.status {
				t.Fatalf("unexpected live endpoint response: HTTP %d; error=%v", result.StatusCode, err)
			}
			if endpoint.status == http.StatusOK && err != nil {
				t.Fatal(err)
			}
			if endpoint.status != http.StatusOK && (!errors.Is(err, ErrHTTPStatus) || errors.Is(err, ErrNetwork)) {
				t.Fatal("live HTTP response was confused with transport failure", err)
			}
			t.Logf("HTTP %d, duration=%s, transportFailure=%t", result.StatusCode, result.Duration, errors.Is(err, ErrNetwork))
		})
	}
}

func TestInvalidEnvironmentNeverSilentlyGoesDirect(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTP_PROXY", "http://[invalid")
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	if _, err := systemProxy(req); !errors.Is(err, ErrProxy) {
		t.Fatalf("invalid proxy was ignored: %v", err)
	}
}

func TestHTTPConnectProxyAuthentication(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Proxy-Authorization") != "" {
			t.Error("CONNECT credentials entered TLS origin")
		}
		fmt.Fprint(writer, "tls-through-proxy")
	}))
	defer origin.Close()
	var connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect {
			writer.WriteHeader(400)
			return
		}
		connects.Add(1)
		if req.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")) {
			writer.WriteHeader(407)
			return
		}
		upstream, err := net.DialTimeout("tcp", req.Host, time.Second)
		if err != nil {
			writer.WriteHeader(502)
			return
		}
		connection, buffer, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		go func() { _, _ = io.Copy(upstream, buffer); upstream.Close() }()
		_, _ = io.Copy(connection, upstream)
		connection.Close()
	}))
	defer proxy.Close()
	manager := newManager(t, Config{Mode: Custom, Address: proxy.URL, CredentialID: "https"}, memoryVault{Credentials{"user", "pass"}})
	// 只信任测试服务器 CA，不关闭真实客户端的 TLS 验证。
	manager.Snapshot().transport.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if request(t, manager, origin.URL) != "tls-through-proxy" || connects.Load() != 1 {
		t.Fatal("authenticated CONNECT failed")
	}
}
