package groups

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Resolver resolves a group reference to a list of repo paths.
type Resolver interface {
	Resolve(ctx context.Context, ref string) ([]string, error)
}

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
	if len(resolvers) == 1 {
		return resolvers[0]
	}
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
		return nil, fmt.Errorf("group kind %q not found", kind)
	}
	repos, ok := byName[name]
	if !ok {
		return nil, fmt.Errorf("group %q not found", ref)
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
	}
	return nil, fmt.Errorf("group %q not found", ref)
}
