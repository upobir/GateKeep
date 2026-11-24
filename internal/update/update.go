// Package update checks for newer published GateKeep releases.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const latestReleaseURL = "https://api.github.com/repos/upobir/GateKeep/releases/latest"

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Release struct {
	Version string
	URL     string
}

// Check returns nil when current is not a release version or no newer release exists.
func Check(ctx context.Context, current string) (*Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return check(ctx, http.DefaultClient, latestReleaseURL, current)
}

func check(ctx context.Context, client *http.Client, endpoint, current string) (*Release, error) {
	if !versionPattern.MatchString(current) {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "GateKeep/"+current)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // No published releases yet.
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub release check: HTTP %d", resp.StatusCode)
	}
	var latest struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&latest); err != nil {
		return nil, fmt.Errorf("decode GitHub release: %w", err)
	}
	if latest.Draft || latest.Prerelease || !newer(latest.Tag, current) {
		return nil, nil
	}
	return &Release{
		Version: latest.Tag,
		URL:     "https://github.com/upobir/GateKeep/releases/tag/" + latest.Tag,
	}, nil
}

func newer(candidate, current string) bool {
	a, b := versionPattern.FindStringSubmatch(candidate), versionPattern.FindStringSubmatch(current)
	if a == nil || b == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		// Compare digit counts first to avoid integer overflow for large versions.
		if len(a[i]) != len(b[i]) {
			return len(a[i]) > len(b[i])
		}
		if a[i] != b[i] {
			return strings.Compare(a[i], b[i]) > 0
		}
	}
	return false
}
