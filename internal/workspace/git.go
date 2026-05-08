package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// cloneConcurrency caps simultaneous clone/pull operations to avoid hitting
// GitHub's secondary rate limits. Empirically four parallel clones keep most
// network/auth setups happy.
const cloneConcurrency = 4

// ensureRepos prepares all repos in parallel (bounded by cloneConcurrency)
// and returns the first error encountered, if any.
func (m *Manager) ensureRepos(ctx context.Context, repos []string) error {
	sem := make(chan struct{}, cloneConcurrency)
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	for _, repo := range repos {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := m.ensureRepo(ctx, repo); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// ensureRepo clones the repo into reposDir if it is not already present,
// or pulls the latest changes if it already exists.
// repo must be an "org/name" path matching a GitHub repository.
// Uses `gh repo clone` for new clones (relying on gh auth) and
// `git -C <dst> pull --ff-only` for updates (gh has no pull equivalent).
// Both paths retry with exponential backoff when GitHub returns a
// rate-limit / secondary rate-limit / abuse-detection signal.
func (m *Manager) ensureRepo(ctx context.Context, repo string) error {
	dst := filepath.Join(m.reposDir, filepath.FromSlash(repo))
	if _, err := os.Stat(dst); err == nil {
		fmt.Printf("Updating %s...\n", repo)
		_, err := runWithRateLimitRetry(ctx, repo, "git pull", func(ctx context.Context) *exec.Cmd {
			return exec.CommandContext(ctx, "git", "-C", dst, "pull", "--ff-only", "--quiet")
		})
		if err != nil {
			fmt.Printf("git pull failed for %s, using existing copy\n", repo)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("prepare repo dir for %q: %v", repo, err)
	}
	fmt.Printf("Cloning %s...\n", repo)
	_, err := runWithRateLimitRetry(ctx, repo, "gh clone", func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "gh", "repo", "clone", repo, dst)
	})
	if err != nil {
		_ = os.RemoveAll(dst)
		return fmt.Errorf("clone %q: %v", repo, err)
	}
	return nil
}

// runWithRateLimitRetry runs the given command with up to three attempts,
// retrying with exponential backoff (5s, 15s) when stderr indicates GitHub
// rate-limiting. Non-rate-limit failures return immediately. Output is
// captured and returned only as part of the error string on terminal failure.
func runWithRateLimitRetry(ctx context.Context, repo, label string, mkCmd func(ctx context.Context) *exec.Cmd) ([]byte, error) {
	const maxAttempts = 3
	backoffs := []time.Duration{5 * time.Second, 15 * time.Second}

	var lastOut []byte
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var buf bytes.Buffer
		cmd := mkCmd(ctx)
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		lastOut = buf.Bytes()
		if err == nil {
			return lastOut, nil
		}
		lastErr = fmt.Errorf("%s: %v: %s", label, err, strings.TrimSpace(buf.String()))
		if !isRateLimitError(buf.String()) || attempt == maxAttempts-1 {
			return lastOut, lastErr
		}
		wait := backoffs[attempt]
		fmt.Printf("%s rate-limited for %s, retrying in %s...\n", label, repo, wait)
		select {
		case <-ctx.Done():
			return lastOut, ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastOut, lastErr
}

// isRateLimitError reports whether output looks like a GitHub rate-limit
// or abuse-detection response.
func isRateLimitError(out string) bool {
	low := strings.ToLower(out)
	for _, marker := range []string{
		"rate limit",
		"secondary rate",
		"abuse detection",
		"too many requests",
		"http 429",
		"status: 429",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}
