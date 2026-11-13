// Package lab supplies loopback-only acceptance infrastructure, never a public proxy.
package lab

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Environment struct {
	Feed, HTTPProxy, SOCKSProxy, Username, Password, PublicKey string
	OriginRequests                                             atomic.Int64
	HTTPRequests                                               atomic.Int64
	SOCKSRequests                                              atomic.Int64
	PayloadRequests                                            atomic.Int64
	LeakedCredentials                                          atomic.Bool
	signature                                                  string
	archive                                                    []byte
	listener                                                   net.Listener
	httpListener                                               net.Listener
	socksListener                                              net.Listener
	server                                                     *http.Server
	proxy                                                      *http.Server
	wait                                                       sync.WaitGroup
	closed                                                     chan struct{}
	mode                                                       atomic.Value
	mu                                                         sync.Mutex
	connections                                                map[net.Conn]bool
	release                                                    ReleaseOptions
}

// ReleaseOptions 只改变本地夹具的展示和速度，不改变真实更新器的验签与切换路径。
type ReleaseOptions struct {
	Version, CurrentVersion, Notes string
	ChunkDelay                     time.Duration
}

func Archive(executable []byte, version string) ([]byte, error) {
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	descriptor, err := json.Marshal(map[string]string{"version": version, "target": "windows-amd64", "executable": "ToolNest.exe"})
	if err != nil {
		return nil, err
	}
	for _, entry := range []struct {
		name string
		data []byte
	}{{".toolnest-release.json", descriptor}, {"ToolNest.exe", executable}} {
		if err = writer.WriteHeader(&tar.Header{Name: entry.name, Mode: 0755, Size: int64(len(entry.data)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err = writer.Write(entry.data); err != nil {
			return nil, err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	if err = compressed.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func Start(executable []byte) (*Environment, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	archive, err := Archive(executable, "0.1.1")
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(archive)
	return StartSigned(archive, base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:])), base64.StdEncoding.EncodeToString(public))
}

func StartSigned(archive []byte, signature, publicKey string) (*Environment, error) {
	return StartSignedRelease(archive, signature, publicKey, ReleaseOptions{
		Version: "0.1.1", CurrentVersion: "0.1.0", Notes: "0.1.1: acceptance-only signed local update",
	})
}

func StartSignedRelease(archive []byte, signature, publicKey string, release ReleaseOptions) (*Environment, error) {
	version := regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	if !version.MatchString(release.Version) || !version.MatchString(release.CurrentVersion) || release.ChunkDelay < 0 || release.ChunkDelay > time.Second {
		return nil, errors.New("invalid manual release configuration")
	}
	public, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return nil, errors.New("invalid fixture public key")
	}
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	digest := sha256.Sum256(archive)
	if err != nil || !ed25519.Verify(public, digest[:], signatureBytes) {
		return nil, errors.New("fixture archive signature mismatch")
	}
	environment := &Environment{signature: signature, archive: archive, PublicKey: publicKey, closed: make(chan struct{}), connections: map[net.Conn]bool{}, release: release}
	credential := make([]byte, 16)
	if _, err = rand.Read(credential); err != nil {
		return nil, err
	}
	environment.Username = "toolnest-acceptance"
	environment.Password = base64.RawURLEncoding.EncodeToString(credential)
	environment.mode.Store("good")
	for _, destination := range []*net.Listener{&environment.listener, &environment.httpListener, &environment.socksListener} {
		*destination, err = net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			environment.Close()
			return nil, err
		}
	}
	environment.Feed = "http://" + environment.listener.Addr().String()
	environment.HTTPProxy = "http://" + environment.httpListener.Addr().String()
	environment.SOCKSProxy = "socks5://" + environment.socksListener.Addr().String()
	environment.server = &http.Server{Handler: http.HandlerFunc(environment.origin), ReadHeaderTimeout: 3 * time.Second}
	environment.proxy = &http.Server{Handler: http.HandlerFunc(environment.forward), ReadHeaderTimeout: 3 * time.Second}
	environment.wait.Add(3)
	go func() { defer environment.wait.Done(); _ = environment.server.Serve(environment.listener) }()
	go func() { defer environment.wait.Done(); _ = environment.proxy.Serve(environment.httpListener) }()
	go environment.acceptSOCKS()
	return environment, nil
}

func (environment *Environment) SetMode(mode string) error {
	switch mode {
	case "good", "bad-signature", "disconnect", "truncated", "http-error", "source-not-found":
		environment.mode.Store(mode)
		return nil
	}
	return errors.New("unsupported fault")
}

func (environment *Environment) origin(writer http.ResponseWriter, request *http.Request) {
	environment.OriginRequests.Add(1)
	if request.Header.Get("Proxy-Authorization") != "" || request.Header.Get("Authorization") != "" {
		environment.LeakedCredentials.Store(true)
	}
	switch request.URL.Path {
	case "/health":
		_, _ = io.WriteString(writer, "toolnest-acceptance\n")
	case "/stats":
		// 只返回计数与泄漏判据，验收不需要导出认证口令或请求头。
		_ = json.NewEncoder(writer).Encode(map[string]any{"httpRequests": environment.HTTPRequests.Load(), "socksRequests": environment.SOCKSRequests.Load(), "payloadRequests": environment.PayloadRequests.Load(), "leakedCredentials": environment.LeakedCredentials.Load()})
	case "/notes":
		_, _ = io.WriteString(writer, environment.release.Notes)
	case "/redirect":
		http.Redirect(writer, request, environment.Feed+"/payload", http.StatusFound)
	case "/manifest", "/current":
		if environment.mode.Load().(string) == "source-not-found" {
			http.NotFound(writer, request)
			return
		}
		signature, _ := base64.StdEncoding.DecodeString(environment.signature)
		if environment.mode.Load().(string) == "bad-signature" {
			signature[0] ^= 1
		}
		writer.Header().Set("Content-Type", "application/json")
		version := environment.release.Version
		if request.URL.Path == "/current" {
			version = environment.release.CurrentVersion
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"version": version, "url": environment.Feed + "/redirect", "notesUrl": environment.Feed + "/notes", "size": len(environment.archive), "signature": base64.StdEncoding.EncodeToString(signature)})
	case "/payload":
		environment.PayloadRequests.Add(1)
		switch environment.mode.Load().(string) {
		case "http-error":
			http.Error(writer, "fixture unavailable", http.StatusServiceUnavailable)
			return
		case "disconnect":
			connection, _, err := writer.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		case "truncated":
			writer.Header().Set("Content-Length", strconv.Itoa(len(environment.archive)))
			_, _ = writer.Write(environment.archive[:len(environment.archive)/2])
			return
		}
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.Header().Set("Content-Length", strconv.Itoa(len(environment.archive)))
		for offset := 0; offset < len(environment.archive); {
			end := min(offset+32*1024, len(environment.archive))
			if _, err := writer.Write(environment.archive[offset:end]); err != nil {
				return
			}
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			offset = end
			if environment.release.ChunkDelay > 0 {
				select {
				case <-time.After(environment.release.ChunkDelay):
				case <-request.Context().Done():
					return
				case <-environment.closed:
					return
				}
			}
		}
	default:
		http.NotFound(writer, request)
	}
}

func (environment *Environment) allowed(address string) bool {
	// 只允许当前夹具自己的源端口，拒绝转发到公网、宿主其他服务和 DNS 名称。
	return address == environment.listener.Addr().String()
}

func (environment *Environment) forward(writer http.ResponseWriter, request *http.Request) {
	environment.HTTPRequests.Add(1)
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(environment.Username+":"+environment.Password))
	if subtle.ConstantTimeCompare([]byte(request.Header.Get("Proxy-Authorization")), []byte(expected)) != 1 {
		writer.Header().Set("Proxy-Authenticate", `Basic realm="ToolNest acceptance"`)
		http.Error(writer, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if request.Method != http.MethodGet || request.URL.Scheme != "http" || request.URL.User != nil || !environment.allowed(request.URL.Host) {
		http.Error(writer, "fixture target only", http.StatusForbidden)
		return
	}
	forwarded := request.Clone(request.Context())
	forwarded.RequestURI = ""
	forwarded.Header = request.Header.Clone()
	forwarded.Header.Del("Proxy-Authorization")
	forwarded.Header.Del("Proxy-Connection")
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	response, err := transport.RoundTrip(forwarded)
	if err != nil {
		http.Error(writer, "fixture target unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
}

func (environment *Environment) acceptSOCKS() {
	defer environment.wait.Done()
	for {
		connection, err := environment.socksListener.Accept()
		if err != nil {
			return
		}
		environment.mu.Lock()
		environment.connections[connection] = true
		environment.mu.Unlock()
		environment.wait.Add(1)
		go func() {
			defer environment.wait.Done()
			defer connection.Close()
			defer func() { environment.mu.Lock(); delete(environment.connections, connection); environment.mu.Unlock() }()
			environment.socks(connection)
		}()
	}
}

func (environment *Environment) socks(connection net.Conn) {
	environment.SOCKSRequests.Add(1)
	timeout := 10 * time.Second
	if environment.release.ChunkDelay > 0 {
		timeout = 2 * time.Minute
	}
	_ = connection.SetDeadline(time.Now().Add(timeout))
	reader := bufio.NewReader(connection)
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 5 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return
	}
	if !bytes.Contains(methods, []byte{2}) {
		_, _ = connection.Write([]byte{5, 255})
		return
	}
	_, _ = connection.Write([]byte{5, 2})
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 1 {
		return
	}
	username := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, username); err != nil {
		return
	}
	length, err := reader.ReadByte()
	if err != nil {
		return
	}
	secretBytes := make([]byte, int(length))
	if _, err = io.ReadFull(reader, secretBytes); err != nil {
		return
	}
	if subtle.ConstantTimeCompare(username, []byte(environment.Username)) != 1 || subtle.ConstantTimeCompare(secretBytes, []byte(environment.Password)) != 1 {
		_, _ = connection.Write([]byte{1, 1})
		return
	}
	_, _ = connection.Write([]byte{1, 0})
	command := make([]byte, 4)
	if _, err = io.ReadFull(reader, command); err != nil || command[0] != 5 || command[1] != 1 || command[2] != 0 {
		return
	}
	var host string
	switch command[3] {
	case 1:
		address := make([]byte, 4)
		if _, err = io.ReadFull(reader, address); err != nil {
			return
		}
		host = net.IP(address).String()
	case 4:
		address := make([]byte, 16)
		if _, err = io.ReadFull(reader, address); err != nil {
			return
		}
		host = net.IP(address).String()
	case 3:
		size, readErr := reader.ReadByte()
		if readErr != nil {
			return
		}
		address := make([]byte, int(size))
		if _, err = io.ReadFull(reader, address); err != nil {
			return
		}
		host = string(address)
	default:
		return
	}
	port := make([]byte, 2)
	if _, err = io.ReadFull(reader, port); err != nil {
		return
	}
	address := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port))))
	if !environment.allowed(address) {
		_, _ = connection.Write([]byte{5, 2, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	upstream, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	_ = upstream.SetDeadline(time.Now().Add(timeout))
	_, _ = connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	finished := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, reader)
		if tcp, ok := upstream.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		close(finished)
	}()
	_, _ = io.Copy(connection, upstream)
	_ = connection.Close()
	_ = upstream.Close()
	<-finished
}

func (environment *Environment) Close() {
	if environment == nil {
		return
	}
	select {
	case <-environment.closed:
		return
	default:
		close(environment.closed)
	}
	if environment.server != nil {
		_ = environment.server.Close()
	}
	if environment.proxy != nil {
		_ = environment.proxy.Close()
	}
	for _, listener := range []net.Listener{environment.listener, environment.httpListener, environment.socksListener} {
		if listener != nil {
			_ = listener.Close()
		}
	}
	environment.mu.Lock()
	for connection := range environment.connections {
		_ = connection.Close()
	}
	environment.mu.Unlock()
	environment.wait.Wait()
}

func (environment *Environment) WriteConfig(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	// 凭据仅存在外部工作目录；证据摘要不含密码，且绝不把私钥写进映射给客体的输入。
	data, err := json.MarshalIndent(map[string]string{"feed": environment.Feed + "/manifest", "httpProxy": environment.HTTPProxy, "socksProxy": environment.SOCKSProxy, "username": environment.Username, "credential": environment.Password, "publicKey": environment.PublicKey}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "fixture-private.json"), data, 0600)
}

func LoopbackURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "http" && parsed.User == nil && net.ParseIP(parsed.Hostname()) != nil && net.ParseIP(parsed.Hostname()).IsLoopback() && !strings.Contains(raw, "#")
}

func (environment *Environment) ServeUntil(ctx context.Context) error {
	<-ctx.Done()
	environment.Close()
	return nil
}

func (environment *Environment) String() string {
	return fmt.Sprintf("loopback feed=%s proxy requests=%d/%d", environment.Feed, environment.HTTPRequests.Load(), environment.SOCKSRequests.Load())
}
