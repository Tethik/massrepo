package groups

import (
	"context"
	"encoding/json"
	"errors"
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

func TestGithubResolver(t *testing.T) {
	// authLogin is what `gh api user --jq .login` returns in these cases —
	// "someone-else" so user:<name> queries treat the target as a third
	// party (using the public /users/<name>/repos endpoint).
	const authLogin = "someone-else"
	for _, tt := range []struct {
		name     string
		ref      string
		wantArgs []string
		stdout   string
		want     []string
	}{
		{
			name:     "org default filters archived and forks",
			ref:      "org:acme",
			wantArgs: []string{"api", "orgs/acme/repos", "--paginate", "--jq", ".[] | select(.archived == false and .fork == false) | .full_name"},
			stdout:   "acme/a\nacme/b\n",
			want:     []string{"acme/a", "acme/b"},
		},
		{
			name:     "user default filters archived and forks",
			ref:      "user:tethik",
			wantArgs: []string{"api", "users/tethik/repos", "--paginate", "--jq", ".[] | select(.archived == false and .fork == false) | .full_name"},
			stdout:   "tethik/x\n",
			want:     []string{"tethik/x"},
		},
		{
			name:     "+archived drops archived predicate",
			ref:      "org:acme+archived",
			wantArgs: []string{"api", "orgs/acme/repos", "--paginate", "--jq", ".[] | select(.fork == false) | .full_name"},
			stdout:   "acme/a\n",
			want:     []string{"acme/a"},
		},
		{
			name:     "+forks drops fork predicate",
			ref:      "org:acme+forks",
			wantArgs: []string{"api", "orgs/acme/repos", "--paginate", "--jq", ".[] | select(.archived == false) | .full_name"},
			stdout:   "acme/a\n",
			want:     []string{"acme/a"},
		},
		{
			name:     "+all drops all predicates",
			ref:      "org:acme+all",
			wantArgs: []string{"api", "orgs/acme/repos", "--paginate", "--jq", ".[] | .full_name"},
			stdout:   "acme/a\nacme/b\n",
			want:     []string{"acme/a", "acme/b"},
		},
		{
			name:     "+archived+forks equivalent to +all",
			ref:      "org:acme+archived+forks",
			wantArgs: []string{"api", "orgs/acme/repos", "--paginate", "--jq", ".[] | .full_name"},
			stdout:   "acme/a\n",
			want:     []string{"acme/a"},
		},
		{
			name:     "blank lines in output ignored",
			ref:      "user:tethik+all",
			wantArgs: []string{"api", "users/tethik/repos", "--paginate", "--jq", ".[] | .full_name"},
			stdout:   "\ntethik/a\n\ntethik/b\n",
			want:     []string{"tethik/a", "tethik/b"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var gotArgs []string
			r := &githubResolver{
				run: func(_ context.Context, args ...string) ([]byte, error) {
					if len(args) >= 2 && args[0] == "api" && args[1] == "user" {
						return []byte(authLogin + "\n"), nil
					}
					gotArgs = args
					return []byte(tt.stdout), nil
				},
			}
			got, err := r.Resolve(context.Background(), tt.ref)
			require.NoError(t, err)
			assert.Equal(t, tt.wantArgs, gotArgs)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGithubResolver_SelfUserUsesPrivateEndpoint(t *testing.T) {
	var gotArgs []string
	r := &githubResolver{
		run: func(_ context.Context, args ...string) ([]byte, error) {
			if len(args) >= 2 && args[0] == "api" && args[1] == "user" {
				return []byte("Tethik\n"), nil
			}
			gotArgs = args
			return []byte("tethik/a\ntethik/b\n"), nil
		},
	}
	got, err := r.Resolve(context.Background(), "user:tethik")
	require.NoError(t, err)
	// /user/repos with affiliation=owner returns repos including private.
	assert.Equal(t, []string{
		"api", "user/repos?affiliation=owner",
		"--paginate", "--jq", ".[] | select(.archived == false and .fork == false) | .full_name",
	}, gotArgs)
	assert.Equal(t, []string{"tethik/a", "tethik/b"}, got)
}

func TestGithubResolver_AuthCheckFailureFallsBack(t *testing.T) {
	var gotArgs []string
	r := &githubResolver{
		run: func(_ context.Context, args ...string) ([]byte, error) {
			if len(args) >= 2 && args[0] == "api" && args[1] == "user" {
				return nil, errors.New("not authenticated")
			}
			gotArgs = args
			return []byte("tethik/a\n"), nil
		},
	}
	got, err := r.Resolve(context.Background(), "user:tethik")
	require.NoError(t, err)
	// Falls back to public endpoint when auth check fails.
	assert.Equal(t, "users/tethik/repos", gotArgs[1])
	assert.Equal(t, []string{"tethik/a"}, got)
}

func TestGithubResolver_Errors(t *testing.T) {
	calls := 0
	noRun := func(_ context.Context, _ ...string) ([]byte, error) {
		calls++
		return nil, nil
	}

	t.Run("unsupported kind returns ErrUnhandled", func(t *testing.T) {
		calls = 0
		r := &githubResolver{run: noRun}
		_, err := r.Resolve(context.Background(), "team:foo")
		assert.ErrorIs(t, err, ErrUnhandled)
		assert.Equal(t, 0, calls)
	})

	t.Run("unknown modifier rejected before exec", func(t *testing.T) {
		calls = 0
		r := &githubResolver{run: noRun}
		_, err := r.Resolve(context.Background(), "org:acme+bogus")
		assert.ErrorContains(t, err, "unknown modifier")
		assert.Equal(t, 0, calls)
	})

	t.Run("missing name", func(t *testing.T) {
		calls = 0
		r := &githubResolver{run: noRun}
		_, err := r.Resolve(context.Background(), "org:")
		assert.ErrorContains(t, err, "missing name")
		assert.Equal(t, 0, calls)
	})

	t.Run("empty result errors", func(t *testing.T) {
		r := &githubResolver{
			run: func(_ context.Context, _ ...string) ([]byte, error) {
				return []byte(""), nil
			},
		}
		_, err := r.Resolve(context.Background(), "org:acme")
		assert.ErrorContains(t, err, "matched no repos")
	})
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
	assert.ErrorIs(t, err, ErrUnhandled)
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
