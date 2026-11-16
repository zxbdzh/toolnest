package update

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"toolnest/internal/network"
)

const maxMetadata = 1 << 20

// Source is deliberately sealed: production is GitHub HTTPS, tests loopback HTTP.
type Source interface {
	latest(context.Context, *network.Client) (*Release, error)
	allow(string) bool
	Endpoint() string
}

type Release struct {
	Version   string  `json:"version"`
	Notes     string  `json:"notes,omitempty"`
	NotesURL  string  `json:"notesUrl,omitempty"`
	URL       string  `json:"url"`
	Size      int64   `json:"size"`
	Signature string  `json:"signature"`
	Deltas    []Delta `json:"deltas,omitempty"`
}
type Delta struct {
	From      string `json:"from"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}

type githubSource struct{ repository, asset string }

func GitHub(repository string) (Source, error) {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 {
		return nil, ErrNotConfigured
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, ErrNotConfigured
		}
		for _, character := range part {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.') {
				return nil, ErrNotConfigured
			}
		}
	}
	return githubSource{repository: repository, asset: "update-windows-amd64.json"}, nil
}

func (source githubSource) allow(raw string) bool {
	address, err := url.Parse(raw)
	return err == nil && address.Scheme == "https" && address.Host != "" && address.User == nil && address.Fragment == ""
}
func (source githubSource) Endpoint() string {
	return "https://github.com/" + source.repository + "/releases/latest/download/" + source.asset
}

func (source githubSource) latest(ctx context.Context, client *network.Client) (*Release, error) {
	// latest/download 由 GitHub 解析最新正式发行，不消耗共享出口的匿名 API 配额。
	// 仍只走用户配置的客户端；大小、稳定版本、归档签名和归档内版本绑定由原有流程核验。
	return readManifest(ctx, client, source.Endpoint(), source.allow)
}

type testSource struct{ endpoint string }

func (source testSource) Endpoint() string { return source.endpoint }

// HTTPTestSource intentionally accepts only numeric loopback addresses.
func HTTPTestSource(endpoint string) (Source, error) {
	source := testSource{endpoint: endpoint}
	if !source.allow(endpoint) {
		return nil, errors.New("test update source must use a numeric loopback HTTP address")
	}
	return source, nil
}
func (source testSource) allow(raw string) bool {
	address, err := url.Parse(raw)
	if err != nil || address.Scheme != "http" || address.User != nil || address.Fragment != "" {
		return false
	}
	ip := net.ParseIP(address.Hostname())
	return ip != nil && ip.IsLoopback()
}
func (source testSource) latest(ctx context.Context, client *network.Client) (*Release, error) {
	return readManifest(ctx, client, source.endpoint, source.allow)
}

func fetch(ctx context.Context, client *network.Client, endpoint string, limit int64, allow func(string) bool) ([]byte, error) {
	response, err := open(ctx, client, endpoint, allow)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.ContentLength > limit {
		return nil, ErrSize
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, network.ErrNetwork
	}
	if int64(len(data)) > limit {
		return nil, ErrSize
	}
	return data, nil
}
func open(ctx context.Context, client *network.Client, endpoint string, allow func(string) bool) (*http.Response, error) {
	if !allow(endpoint) {
		return nil, errors.New("update URL does not match the configured source security policy")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, network.ErrInvalidConfig
	}
	req.Header.Set("Accept", "application/json, application/octet-stream")
	req.Header.Set("User-Agent", "ToolNest-Updater/1")
	response, err := client.WithURLPolicy(allow).Do(req)
	if err != nil {
		return nil, err
	}
	if !allow(response.Request.URL.String()) {
		response.Body.Close()
		return nil, network.ErrRedirect
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, &network.HTTPStatusError{StatusCode: response.StatusCode}
	}
	return response, nil
}
func readManifest(ctx context.Context, client *network.Client, endpoint string, allow func(string) bool) (*Release, error) {
	data, err := fetch(ctx, client, endpoint, maxMetadata, allow)
	if err != nil {
		return nil, err
	}
	var release Release
	if json.Unmarshal(data, &release) != nil {
		return nil, errors.New("invalid release manifest")
	}
	if !allow(release.URL) {
		return nil, ErrArchive
	}
	if release.NotesURL != "" {
		notes, err := fetch(ctx, client, release.NotesURL, maxMetadata, allow)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// 说明暂不可用不能阻断签名包的检查；保留内嵌说明或明确空态。
			return &release, nil
		}
		release.Notes = string(notes)
	}
	return &release, nil
}
func validateRelease(release *Release, limit int64) error {
	if release == nil {
		return ErrVersion
	}
	if _, err := stableVersion(release.Version); err != nil {
		return err
	}
	if release.Size <= 0 || release.Size > limit {
		return ErrSize
	}
	if release.URL == "" || release.Signature == "" {
		return ErrArchive
	}
	return nil
}
