package groups

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// githubResolver expands "org:<name>" and "user:<name>" refs to a list of
// GitHub repos owned by that account, using the gh CLI. Optional "+modifier"
// suffixes control filtering: "+archived" includes archived repos,
// "+forks" includes forks, "+all" includes both.
type githubResolver struct {
	// run executes the gh CLI and returns combined stdout. Injectable for tests.
	run func(ctx context.Context, args ...string) ([]byte, error)
}

func newGithubResolver() *githubResolver {
	return &githubResolver{run: ghRun}
}

func ghRun(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh %s: %v: %s",
				strings.Join(args, " "), err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("gh %s: %v", strings.Join(args, " "), err)
	}
	return out, nil
}

func (r *githubResolver) Resolve(ctx context.Context, ref string) ([]string, error) {
	kind, rest, ok := strings.Cut(ref, ":")
	if !ok {
		return nil, fmt.Errorf("invalid group ref %q: expected kind:name", ref)
	}

	switch kind {
	case "org", "user":
	default:
		return nil, ErrUnhandled
	}

	parts := strings.Split(rest, "+")
	name := parts[0]
	if name == "" {
		return nil, fmt.Errorf("invalid github ref %q: missing name", ref)
	}

	includeArchived, includeForks := false, false
	for _, mod := range parts[1:] {
		switch mod {
		case "archived":
			includeArchived = true
		case "forks":
			includeForks = true
		case "all":
			includeArchived, includeForks = true, true
		default:
			return nil, fmt.Errorf("invalid github ref %q: unknown modifier %q", ref, mod)
		}
	}

	jq := buildGithubJQ(includeArchived, includeForks)
	args := []string{"api"}
	switch kind {
	case "org":
		args = append(args, "orgs/"+name+"/repos")
	case "user":
		// GitHub's public /users/<name>/repos endpoint omits private repos
		// even when authenticated. When the requested user is the
		// gh-authenticated user, switch to /user/repos which includes
		// private repos for accounts the caller owns.
		if self, err := r.authenticatedUser(ctx); err == nil && strings.EqualFold(self, name) {
			// Embed query params in the path: passing them via `-f` makes
			// `gh api` default to POST, which 422s on /user/repos.
			args = append(args, "user/repos?affiliation=owner")
		} else {
			args = append(args, "users/"+name+"/repos")
		}
	}
	args = append(args, "--paginate", "--jq", jq)

	out, err := r.run(ctx, args...)
	if err != nil {
		return nil, err
	}

	var repos []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		repos = append(repos, line)
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("group %q matched no repos", ref)
	}
	return repos, nil
}

// authenticatedUser returns the login of the gh-authenticated user, or an
// error if not authenticated.
func (r *githubResolver) authenticatedUser(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// buildGithubJQ produces the jq filter passed to `gh api --jq`.
// Default excludes archived and forks; flags relax the filter.
func buildGithubJQ(includeArchived, includeForks bool) string {
	var preds []string
	if !includeArchived {
		preds = append(preds, ".archived == false")
	}
	if !includeForks {
		preds = append(preds, ".fork == false")
	}
	if len(preds) == 0 {
		return ".[] | .full_name"
	}
	return ".[] | select(" + strings.Join(preds, " and ") + ") | .full_name"
}
