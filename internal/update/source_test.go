package update

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"toolnest/internal/network"
)

func githubFixtureNetwork(t *testing.T, handler http.Handler) *network.Manager {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect {
			http.Error(writer, "CONNECT required", http.StatusBadRequest)
			return
		}
		upstream, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
		if err != nil {
			http.Error(writer, "fixture unavailable", http.StatusBadGateway)
			return
		}
		connection, buffered, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		defer connection.Close()
		defer upstream.Close()
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		go func() { _, _ = io.Copy(upstream, buffered) }()
		_, _ = io.Copy(connection, upstream)
	}))
	t.Cleanup(proxy.Close)
	// 只有本测试的客户端信任夹具证书；真实 GitHub 域名仍经过完整 URL 策略和同一代理。
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	previous := http.DefaultTransport
	transport := previous.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: server.Certificate().DNSNames[0]}
	http.DefaultTransport = transport
	manager, err := network.New(context.Background(), network.Config{Mode: network.Custom, Address: proxy.URL}, nil)
	http.DefaultTransport = previous
	if err != nil {
		t.Fatal(err)
	}
	client := manager.Snapshot()
	t.Cleanup(client.CloseIdleConnections)
	return manager
}

func TestGitHubDiscoveryDoesNotDependOnAPIRateLimit(t *testing.T) {
	var apiCalls, assetCalls atomic.Int32
	manager := githubFixtureNetwork(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host == "api.github.com" {
			apiCalls.Add(1)
			writer.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(writer, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
			return
		}
		if request.Host != "github.com" || request.URL.Path != "/owner/repo/releases/latest/download/update-windows-amd64.json" {
			http.Error(writer, "unexpected endpoint", http.StatusNotFound)
			return
		}
		assetCalls.Add(1)
		_ = json.NewEncoder(writer).Encode(Release{Version: "1.2.3", URL: "https://github.com/owner/repo/releases/download/v1.2.3/package.tar.gz", Size: 123, Signature: "fixture"})
	}))
	source, err := GitHub("owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	release, err := source.latest(context.Background(), manager.Snapshot())
	if err != nil || release == nil || release.Version != "1.2.3" {
		t.Fatalf("public release asset must remain usable when anonymous API is limited: release=%+v error=%v", release, err)
	}
	if apiCalls.Load() != 0 || assetCalls.Load() != 1 {
		t.Fatalf("public discovery unnecessarily consumed API quota: api=%d asset=%d", apiCalls.Load(), assetCalls.Load())
	}
}

func TestGitHubDiscoveryLive(t *testing.T) {
	if os.Getenv("TOOLNEST_LIVE_UPDATE_PROBE") != "1" {
		t.Skip("explicit opt-in: reads public release metadata through the configured local proxy; never installs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	manager, err := network.New(ctx, network.Config{Mode: network.Custom, Address: "http://127.0.0.1:7890"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Snapshot().CloseIdleConnections()
	source, err := GitHub("zxbdzh/toolnest")
	if err != nil {
		t.Fatal(err)
	}
	release, err := source.latest(ctx, manager.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRelease(release, 256<<20); err != nil {
		t.Fatal(err)
	}
	if result, err := manager.TestConnection(ctx, source.Endpoint()); err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("connection probe must use the same working release asset: status=%d error=%v", result.StatusCode, err)
	}
	t.Logf("public discovery through unchanged HTTP proxy: version=%s size=%d", release.Version, release.Size)
}

func TestGitHubDiscoveryRetainsHTTPFailure(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusNotFound} {
		manager := githubFixtureNetwork(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Error(writer, "private response content", code)
		}))
		source, _ := GitHub("owner/repo")
		_, err := source.latest(context.Background(), manager.Snapshot())
		var status *network.HTTPStatusError
		if !errors.As(err, &status) || status.StatusCode != code || errors.Is(err, network.ErrProxy) || strings.Contains(err.Error(), "private") {
			t.Fatalf("HTTP %d must retain status without exposing response contents or blaming the proxy: %v", code, err)
		}
	}
}

func TestGitHubRedirectedReleaseStillVerifiesSignedArchive(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		t.Run(map[bool]string{false: "signed", true: "tampered"}[tamper], func(t *testing.T) {
			fixture := newFixture(t)
			fixture.release.URL = "https://github.com/owner/repo/releases/download/v1.1.0/update.tar.gz"
			fixture.release.NotesURL = ""
			var calls atomic.Int32
			manager := githubFixtureNetwork(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				switch request.Host + request.URL.Path {
				case "github.com/owner/repo/releases/latest/download/update-windows-amd64.json":
					http.Redirect(writer, request, "https://release-assets.githubusercontent.com/fixture/update.json", http.StatusFound)
				case "release-assets.githubusercontent.com/fixture/update.json":
					_ = json.NewEncoder(writer).Encode(fixture.release)
				case "github.com/owner/repo/releases/download/v1.1.0/update.tar.gz":
					http.Redirect(writer, request, "https://release-assets.githubusercontent.com/fixture/archive", http.StatusFound)
				case "release-assets.githubusercontent.com/fixture/archive":
					archive := append([]byte(nil), fixture.archive...)
					if tamper {
						archive[len(archive)-1] ^= 1
					}
					_, _ = writer.Write(archive)
				default:
					http.Error(writer, "unexpected host or route", http.StatusForbidden)
				}
			}))
			source, _ := GitHub("owner/repo")
			fixture.manager.config.Source, fixture.manager.config.Network = source, manager
			if _, err := fixture.manager.Check(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			prepared, err := fixture.manager.Prepare(context.Background())
			fixture.assertOld()
			if tamper {
				if !errors.Is(err, ErrSignature) || prepared != nil {
					t.Fatalf("public metadata must never authorize a forged archive: prepared=%+v error=%v", prepared, err)
				}
			} else if err != nil || prepared == nil || fixture.manager.Status().Phase != Ready {
				t.Fatalf("signed redirected archive did not reach ready: prepared=%+v error=%v", prepared, err)
			}
			if calls.Load() != 4 {
				t.Fatalf("metadata and archive must stay on the same HTTPS proxy snapshot: calls=%d", calls.Load())
			}
		})
	}
}

func TestGitHubRejectsInvalidMetadataAndDowngrade(t *testing.T) {
	for _, scenario := range []string{"unstable-version", "unsafe-package-url", "oversize", "invalid-json", "http-redirect"} {
		t.Run(scenario, func(t *testing.T) {
			manager := githubFixtureNetwork(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				release := Release{Version: "1.1.0", Size: 123, Signature: "fixture", URL: "https://github.com/owner/repo/releases/download/v1.1.0/update.tar.gz"}
				switch scenario {
				case "unstable-version":
					release.Version = "1.1.0-beta"
				case "unsafe-package-url":
					release.URL = "http://example.com/package.tar.gz"
				case "oversize":
					_, _ = io.WriteString(writer, strings.Repeat(" ", maxMetadata+1))
					return
				case "invalid-json":
					_, _ = io.WriteString(writer, "not JSON")
					return
				case "http-redirect":
					http.Redirect(writer, request, "http://example.com/update.json", http.StatusFound)
					return
				}
				_ = json.NewEncoder(writer).Encode(release)
			}))
			fixture := newFixture(t)
			source, _ := GitHub("owner/repo")
			fixture.manager.config.Source, fixture.manager.config.Network = source, manager
			if _, err := fixture.manager.Check(context.Background(), true); err == nil || fixture.manager.Status().Phase == Latest {
				t.Fatalf("unsafe metadata reported success: scenario=%s error=%v phase=%s", scenario, err, fixture.manager.Status().Phase)
			}
			fixture.assertOld()
		})
	}
}
