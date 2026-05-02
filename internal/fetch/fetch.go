package fetch

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Fetch retrieves configuration data from a URL or file path
// Supports:
// - file:// URLs for local files
// - https:// URLs with optional authorization tokens
// - GitHub API URLs with Accept header for raw content
//
// Network-stack agnosticism: the fetcher first tries dual-stack
// (Happy Eyeballs — Go's default) which prefers IPv6 when both are
// available. If that fails, it transparently retries the request
// over an IPv4-only transport. Some hosting environments — notably
// fly.io's iad region reaching raw.githubusercontent.com over IPv6 —
// see persistent connection-reset errors on the v6 path while v4
// works fine. Without the fallback, an entire endpoint can fail to
// boot purely because of an upstream IPv6 issue it has no control
// over. The retry is scoped to the same URL/headers/timeout so it
// matches the dual-stack attempt for everything except address
// family.
func Fetch(configURL, authToken string) ([]byte, error) {
	if configURL == "" {
		return nil, fmt.Errorf("config URL is required")
	}

	// Handle local file URLs
	if strings.HasPrefix(configURL, "file://") {
		filePath := strings.TrimPrefix(configURL, "file://")
		body, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read local file %s: %w", filePath, err)
		}
		return body, nil
	}

	body, err := fetchHTTP(configURL, authToken, "") // dual-stack
	if err == nil {
		return body, nil
	}

	// Retry with IPv4-only when the first attempt looked like a
	// transport-level failure (refused/reset/timeout/etc.) rather
	// than a non-2xx HTTP response. Non-2xx is a server decision
	// that won't change with a different IP family, so we skip the
	// retry to avoid masking real config errors.
	if isTransportError(err) {
		body4, err4 := fetchHTTP(configURL, authToken, "tcp4")
		if err4 == nil {
			return body4, nil
		}
		return nil, fmt.Errorf("dual-stack: %v; ipv4-only: %w", err, err4)
	}

	return nil, err
}

// fetchHTTP performs a single GET. forceNetwork == "" uses Go's
// default dual-stack dialer; "tcp4" or "tcp6" pins the address family.
func fetchHTTP(configURL, authToken, forceNetwork string) ([]byte, error) {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if forceNetwork != "" {
		dialer := &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, forceNetwork, addr)
		}
	}

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}

	req, err := http.NewRequest("GET", configURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if authToken != "" {
		if strings.HasPrefix(authToken, "Bearer ") || strings.HasPrefix(authToken, "token ") {
			req.Header.Set("Authorization", authToken)
		} else {
			req.Header.Set("Authorization", "token "+authToken)
		}
	}

	if strings.Contains(configURL, "api.github.com") {
		req.Header.Set("Accept", "application/vnd.github.v3.raw")
	}
	req.Header.Set("User-Agent", "HSTLES-Platform/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch config, status: %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	return body, nil
}

// isTransportError reports whether err looks like a network-layer
// failure (where retrying on a different address family might help)
// vs a server-returned non-2xx (where it won't). Conservative — when
// in doubt, return true so the retry attempt happens.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Server-decision signals: status code formatted by fetchHTTP.
	if strings.Contains(msg, "status:") {
		return false
	}
	// Anything else is some flavour of dial/TLS/read failure that
	// could plausibly be address-family-specific.
	return true
}
