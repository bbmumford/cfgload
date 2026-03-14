package fetch

import (
	"fmt"
	"io"
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

	// Handle remote URLs
	if authToken == "" && strings.HasPrefix(configURL, "https://") {
		// Allow unauthenticated HTTPS for public endpoints
		// This is useful for public configuration endpoints
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request
	req, err := http.NewRequest("GET", configURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers
	if authToken != "" {
		// Support both "Bearer" and "token" prefix
		if strings.HasPrefix(authToken, "Bearer ") || strings.HasPrefix(authToken, "token ") {
			req.Header.Set("Authorization", authToken)
		} else {
			req.Header.Set("Authorization", "token "+authToken)
		}
	}

	// Special handling for GitHub API URLs
	if strings.Contains(configURL, "api.github.com") {
		req.Header.Set("Accept", "application/vnd.github.v3.raw")
	}

	req.Header.Set("User-Agent", "HSTLES-Platform/2.0")

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch config: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch config, status: %d %s", resp.StatusCode, resp.Status)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return body, nil
}
