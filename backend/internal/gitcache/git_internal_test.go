package gitcache

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGitIdentityEnv_keepsEmailWhenNameEmpty verifies gitIdentityEnv falls
// back per-field rather than per-pair: a caller that supplies a known email
// but an empty name (e.g. a stored git credential with git_email set but
// git_name left blank) must keep that email, not have it discarded in favor
// of the fixed pubobs@localhost default just because the name was empty.
// This lives in an internal (package gitcache) test file — git_test.go is
// package gitcache_test — because gitIdentityEnv is unexported.
func TestGitIdentityEnv_keepsEmailWhenNameEmpty(t *testing.T) {
	env := gitIdentityEnv("", "alice@example.com", "", "")
	joined := strings.Join(env, "\n")
	require.Contains(t, joined, "GIT_AUTHOR_EMAIL=alice@example.com")
	require.NotContains(t, joined, "GIT_AUTHOR_EMAIL=pubobs@localhost")
}

// TestGitIdentityEnv_keepsEmailWhenCommitterNameEmpty mirrors the author-side
// test above for the committer fields.
func TestGitIdentityEnv_keepsEmailWhenCommitterNameEmpty(t *testing.T) {
	env := gitIdentityEnv("Alice", "alice@example.com", "", "bob@example.com")
	joined := strings.Join(env, "\n")
	require.Contains(t, joined, "GIT_COMMITTER_EMAIL=bob@example.com")
	require.NotContains(t, joined, "GIT_COMMITTER_EMAIL=pubobs@localhost")
	require.Contains(t, joined, "GIT_COMMITTER_NAME=pubobs")
}

// TestGitIdentityEnv_bothEmptyStillDefaults verifies the pubobs default is
// preserved for the ordinary case (no editor identity available at all),
// unaffected by this change.
func TestGitIdentityEnv_bothEmptyStillDefaults(t *testing.T) {
	env := gitIdentityEnv("", "", "", "")
	joined := strings.Join(env, "\n")
	require.Contains(t, joined, "GIT_AUTHOR_NAME=pubobs")
	require.Contains(t, joined, "GIT_AUTHOR_EMAIL=pubobs@localhost")
	require.Contains(t, joined, "GIT_COMMITTER_NAME=pubobs")
	require.Contains(t, joined, "GIT_COMMITTER_EMAIL=pubobs@localhost")
}
