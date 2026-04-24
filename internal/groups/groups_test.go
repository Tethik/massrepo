package groups

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsGroupRef(t *testing.T) {
	assert.True(t, IsGroupRef("team:myteam"))
	assert.True(t, IsGroupRef("system:booking-api"))
	assert.False(t, IsGroupRef("org/repo"))
	assert.False(t, IsGroupRef("repo"))
}

func TestStaticResolver(t *testing.T) {
	r := &staticResolver{
		groups: map[string]map[string][]string{
			"team":   {"myteam": []string{"org/repo1", "org/repo2"}},
			"system": {"booking-api": []string{"org/service"}},
		},
	}

	for _, tt := range []struct {
		ref  string
		want []string
	}{
		{"team:myteam", []string{"org/repo1", "org/repo2"}},
		{"system:booking-api", []string{"org/service"}},
	} {
		t.Run(tt.ref, func(t *testing.T) {
			got, err := r.Resolve(context.Background(), tt.ref)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStaticResolver_NotFound(t *testing.T) {
	r := &staticResolver{groups: map[string]map[string][]string{}}
	_, err := r.Resolve(context.Background(), "team:unknown")
	assert.Error(t, err)
}

func TestChainResolver(t *testing.T) {
	empty := &staticResolver{groups: map[string]map[string][]string{}}
	full := &staticResolver{
		groups: map[string]map[string][]string{
			"team": {"myteam": []string{"org/repo1"}},
		},
	}
	chain := &chainResolver{resolvers: []Resolver{empty, full}}

	got, err := chain.Resolve(context.Background(), "team:myteam")
	require.NoError(t, err)
	assert.Equal(t, []string{"org/repo1"}, got)
}

func TestChainResolver_AllFail(t *testing.T) {
	empty := &staticResolver{groups: map[string]map[string][]string{}}
	chain := &chainResolver{resolvers: []Resolver{empty}}
	_, err := chain.Resolve(context.Background(), "team:unknown")
	assert.ErrorContains(t, err, "not found")
}

func TestBackstageResolver(t *testing.T) {
	entities := []map[string]any{
		// project-slug annotation.
		{"metadata": map[string]any{"annotations": map[string]any{
			"github.com/project-slug": "org/service-a",
		}}},
		// Duplicate slug — should be deduplicated.
		{"metadata": map[string]any{"annotations": map[string]any{
			"github.com/project-slug": "org/service-a",
		}}},
		// Split owner+repo annotations.
		{"metadata": map[string]any{"annotations": map[string]any{
			"github.com/owner": "org",
			"github.com/repo":  "service-b",
		}}},
		// view-url fallback (no GitHub annotations).
		{"metadata": map[string]any{"annotations": map[string]any{
			"backstage.io/view-url": "https://github.com/org/service-c/tree/master/.backstage/component.yaml",
		}}},
		// managed-by-location fallback with url: prefix.
		{"metadata": map[string]any{"annotations": map[string]any{
			"backstage.io/managed-by-location": "url:https://github.com/org/service-d/blob/master/.backstage/component.yaml",
		}}},
		// No useful annotations — should be skipped.
		{"metadata": map[string]any{"annotations": map[string]any{}}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "bearer testtoken", r.Header.Get("Authorization"))
		assert.Contains(t, r.URL.RawQuery, "kind=Component,spec.system=mybooking")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entities) //nolint:errcheck
	}))
	defer srv.Close()

	r := &backstageResolver{
		baseURL: srv.URL,
		token:   "testtoken",
		client:  srv.Client(),
	}

	got, err := r.Resolve(context.Background(), "system:mybooking")
	require.NoError(t, err)
	assert.Equal(t, []string{"org/service-a", "org/service-b", "org/service-c", "org/service-d"}, got)
}

func TestRepoSlug(t *testing.T) {
	for _, tt := range []struct {
		name        string
		annotations map[string]string
		want        string
	}{
		{
			name:        "project-slug",
			annotations: map[string]string{"github.com/project-slug": "org/repo"},
			want:        "org/repo",
		},
		{
			name:        "owner+repo",
			annotations: map[string]string{"github.com/owner": "org", "github.com/repo": "repo"},
			want:        "org/repo",
		},
		{
			name:        "view-url",
			annotations: map[string]string{"backstage.io/view-url": "https://github.com/org/repo/tree/master/.backstage/component.yaml"},
			want:        "org/repo",
		},
		{
			name:        "managed-by-location with url: prefix",
			annotations: map[string]string{"backstage.io/managed-by-location": "url:https://github.com/org/repo/blob/master/.backstage/component.yaml"},
			want:        "org/repo",
		},
		{
			name:        "project-slug takes priority over view-url",
			annotations: map[string]string{"github.com/project-slug": "org/slug", "backstage.io/view-url": "https://github.com/org/other/tree/master/x.yaml"},
			want:        "org/slug",
		},
		{
			name:        "non-github view-url",
			annotations: map[string]string{"backstage.io/view-url": "https://gitlab.com/org/repo/tree/master/x.yaml"},
			want:        "",
		},
		{
			name:        "empty annotations",
			annotations: map[string]string{},
			want:        "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, repoSlug(tt.annotations))
		})
	}
}

func TestBackstageResolver_UnsupportedKind(t *testing.T) {
	r := &backstageResolver{baseURL: "http://localhost", client: &http.Client{}}
	_, err := r.Resolve(context.Background(), "unknown:foo")
	assert.ErrorContains(t, err, "unsupported group kind")
}

func TestNew_StaticOnly(t *testing.T) {
	r := New(map[string]map[string][]string{
		"team": {"a": []string{"org/repo"}},
	}, "", "")
	got, err := r.Resolve(context.Background(), "team:a")
	require.NoError(t, err)
	assert.Equal(t, []string{"org/repo"}, got)
}

func TestNew_WithBackstage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{ //nolint:errcheck
			{"metadata": map[string]any{"annotations": map[string]any{"github.com/project-slug": "org/svc"}}},
		})
	}))
	defer srv.Close()

	r := New(map[string]map[string][]string{}, srv.URL, "")
	// Static resolver fails first; Backstage should succeed.
	got, err := r.Resolve(context.Background(), "team:myteam")
	require.NoError(t, err)
	assert.Equal(t, []string{"org/svc"}, got)
}
