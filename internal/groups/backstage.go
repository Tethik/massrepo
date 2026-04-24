package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type backstageResolver struct {
	baseURL string
	token   string
	client  *http.Client
}

func (r *backstageResolver) Resolve(ctx context.Context, ref string) ([]string, error) {
	kind, name, ok := strings.Cut(ref, ":")
	if !ok {
		return nil, fmt.Errorf("invalid group ref %q: expected kind:name", ref)
	}

	filter, err := backstageFilter(kind, name)
	if err != nil {
		return nil, err
	}

	reqURL := r.baseURL + "/api/catalog/entities?filter=" + filter + "&fields=metadata.annotations"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build backstage request: %v", err)
	}
	if r.token != "" {
		req.Header.Set("Authorization", "bearer "+r.token)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("backstage request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backstage returned status %d for group %q", resp.StatusCode, ref)
	}

	var entities []struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entities); err != nil {
		return nil, fmt.Errorf("decode backstage response: %v", err)
	}

	seen := make(map[string]struct{})
	var repos []string
	for _, e := range entities {
		slug := repoSlug(e.Metadata.Annotations)
		if slug == "" {
			continue
		}
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		repos = append(repos, slug)
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("group %q not found in Backstage", ref)
	}
	return repos, nil
}

// repoSlug extracts the org/repo path from Backstage component annotations.
// Tries annotations in order of reliability:
//  1. github.com/project-slug
//  2. github.com/owner + github.com/repo
//  3. backstage.io/view-url  (https://github.com/<org>/<repo>/...)
//  4. backstage.io/managed-by-location  (url:https://github.com/<org>/<repo>/...)
func repoSlug(annotations map[string]string) string {
	if s := annotations["github.com/project-slug"]; s != "" {
		return s
	}
	if owner, repo := annotations["github.com/owner"], annotations["github.com/repo"]; owner != "" && repo != "" {
		return owner + "/" + repo
	}
	for _, key := range []string{"backstage.io/view-url", "backstage.io/managed-by-location"} {
		if s := slugFromGitHubURL(strings.TrimPrefix(annotations[key], "url:")); s != "" {
			return s
		}
	}
	return ""
}

// slugFromGitHubURL parses a GitHub URL and returns the "org/repo" portion.
// Returns "" if the URL is not a github.com URL or cannot be parsed.
func slugFromGitHubURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host != "github.com" {
		return ""
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

func backstageFilter(kind, name string) (string, error) {
	switch kind {
	case "team":
		return "kind=Component,spec.owner=group:default/" + name, nil
	case "system":
		return "kind=Component,spec.system=" + name, nil
	default:
		return "", fmt.Errorf("unsupported group kind %q: use team or system", kind)
	}
}
