package groups

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Resolver resolves a group reference to a list of repo paths.
type Resolver interface {
	Resolve(ctx context.Context, ref string) ([]string, error)
}

// ErrUnhandled is returned by a Resolver when the ref is outside its
// vocabulary (e.g. an unknown kind, or a kind handled by another resolver).
// chainResolver uses this to skip the resolver and try the next one,
// instead of treating the error as a real failure.
var ErrUnhandled = errors.New("ref not handled by this resolver")

// IsGroupRef reports whether s is a group reference (contains ":").
func IsGroupRef(s string) bool {
	return strings.Contains(s, ":")
}

// New returns a Resolver. Chains static → Backstage when backstageURL is set.
func New(staticGroups map[string]map[string][]string, backstageURL, backstageToken string) Resolver {
	resolvers := []Resolver{&staticResolver{groups: staticGroups}}
	if backstageURL != "" {
		resolvers = append(resolvers, &backstageResolver{
			baseURL: backstageURL,
			token:   backstageToken,
			client:  &http.Client{},
		})
	}
	resolvers = append(resolvers, newGithubResolver())
	return &chainResolver{resolvers: resolvers}
}

type staticResolver struct {
	groups map[string]map[string][]string
}

func (r *staticResolver) Resolve(_ context.Context, ref string) ([]string, error) {
	kind, name, ok := strings.Cut(ref, ":")
	if !ok {
		return nil, fmt.Errorf("invalid group ref %q: expected kind:name", ref)
	}
	byName, ok := r.groups[kind]
	if !ok {
		return nil, ErrUnhandled
	}
	repos, ok := byName[name]
	if !ok {
		return nil, ErrUnhandled
	}
	return repos, nil
}

type chainResolver struct {
	resolvers []Resolver
}

func (r *chainResolver) Resolve(ctx context.Context, ref string) ([]string, error) {
	for _, res := range r.resolvers {
		repos, err := res.Resolve(ctx, ref)
		if err == nil {
			return repos, nil
		}
		if !errors.Is(err, ErrUnhandled) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("group %q not found", ref)
}
