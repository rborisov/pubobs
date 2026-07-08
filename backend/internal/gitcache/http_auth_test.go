package gitcache_test

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
	"github.com/stretchr/testify/require"
)

// newStalledGitServer starts a local HTTP server that accepts every
// connection but never completes a response to any request — the same
// black-hole behavior reported in the incident, where the remote (a
// self-hosted Gogs instance, reachable and DNS-resolvable, confirmed by an
// instant `git ls-remote`) accepted the clone/fetch request but never
// finished it, instead of returning a fast 401/403 for bad or missing
// credentials. requestCount, if non-nil, is incremented for every request
// received, so tests can confirm the client actually reached the server
// (i.e. the failure is a genuine timeout, not some unrelated connection
// error) and can assert on how many attempts were made.
func newStalledGitServer(t *testing.T, requestCount *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestCount != nil {
			requestCount.Add(1)
		}
		<-r.Context().Done()
	}))
	// A handler blocked on r.Context().Done() only unblocks once its
	// connection is closed; httptest.Server.Close alone deliberately leaves
	// still-active connections alone and waits for their handlers to return
	// (see https://golang.org/issue/26893), which would otherwise deadlock
	// test cleanup against our own intentionally-never-returns handler.
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	return srv
}

// TestClone_TimesOutFastInsteadOfHangingOnStalledRemote is the regression
// test for the production incident: repo 494ca9da's remote (self-hosted
// Gogs) was reachable (ls-remote succeeded instantly) but the clone/fetch
// itself never completed — and because GitRunner.run used a bare
// exec.Command with no context/timeout at all, that `git clone` subprocess
// would block indefinitely, bounded only by whatever the OS's TCP defaults
// happened to be, not by any intentional timeout in this codebase. That is
// exactly what turned a fast, clear failure into the reported ~34.49s 502.
//
// This test doesn't need to reproduce the exact mechanism of *why* a real
// git server would stall a request (bad credentials tripping a WAF/rate
// limiter, a silently dropped connection, etc. — all plausible and outside
// what this codebase can control) — it only needs to confirm that *given*
// a remote that stalls, GitRunner now fails fast and with a clear,
// distinguishable error instead of hanging.
func TestClone_TimesOutFastInsteadOfHangingOnStalledRemote(t *testing.T) {
	var requests atomic.Int32
	srv := newStalledGitServer(t, &requests)

	g := gitcache.NewGitRunner()
	g.Timeout = 500 * time.Millisecond

	cloneDir := t.TempDir()
	start := time.Now()
	err := g.Clone(cloneDir, srv.URL+"/remote.git", "", "main")
	elapsed := time.Since(start)

	require.Error(t, err, "clone against a remote that never responds must fail, not succeed")
	require.ErrorIs(t, err, gitcache.ErrGitOpTimedOut,
		"a stalled remote must be reported as a timeout, not an opaque generic git error")
	// Bounded by GitRunner.Timeout plus the git subprocess's cleanup grace
	// period (cmd.WaitDelay, needed because a `git-remote-http` helper
	// grandchild can outlive the killed top-level `git` process) — still a
	// small, fixed bound, unlike the unbounded production hang (34.49s and
	// climbing, bounded only by OS-level TCP defaults) this replaces.
	require.Lessf(t, elapsed, 10*time.Second,
		"clone took %s — it must fail fast (bounded by GitRunner.Timeout+cleanup grace), not hang like the production incident (34.49s, unbounded)", elapsed)
	require.Positive(t, requests.Load(), "the clone must have actually reached the server (proving this isn't failing for an unrelated reason)")
}

// TestFetchReset_TimesOutFastInsteadOfHangingOnStalledRemote covers the other
// half of getOrClone's two paths: a repo that already has a healthy local
// clone (so FetchReset runs, not Clone) but whose remote has since started
// stalling requests (e.g. credentials that used to work were revoked).
func TestFetchReset_TimesOutFastInsteadOfHangingOnStalledRemote(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cloneDir := t.TempDir()
	g := gitcache.NewGitRunner()
	require.NoError(t, g.Clone(cloneDir, bareURL, "", "main"))

	var requests atomic.Int32
	srv := newStalledGitServer(t, &requests)
	g.Timeout = 500 * time.Millisecond

	start := time.Now()
	err := g.FetchReset(cloneDir, srv.URL+"/remote.git", "", "main")
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, gitcache.ErrGitOpTimedOut)
	require.Less(t, elapsed, 10*time.Second)
	require.Positive(t, requests.Load())
}

// TestGetOrClone_AuthFailureBacksOffInsteadOfRetryingEveryRequest is the
// regression test for the interaction between b63dc40's self-heal logic and
// a pre-existing bad-credentials repo: once a network op is classified as an
// auth failure, subsequent requests within AuthFailBackoff must return the
// cached failure immediately rather than re-running (and re-timing-out) the
// exact same doomed clone/fetch on every single incoming API request.
func TestGetOrClone_AuthFailureBacksOffInsteadOfRetryingEveryRequest(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	var requests atomic.Int32
	srv := newAuthRequiredGitServer(t, bareURL, "validuser", "validpass")
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		origHandler.ServeHTTP(w, r)
	})

	origBackoff := gitcache.AuthFailBackoff
	gitcache.AuthFailBackoff = 200 * time.Millisecond
	t.Cleanup(func() { gitcache.AuthFailBackoff = origBackoff })

	cacheDir := t.TempDir()
	cache := gitcache.NewCache(cacheDir)

	repo := &model.Repo{
		ID:             "auth-broken-repo",
		RemoteURL:      srv.URL + "/remote.git",
		EncryptedCreds: "", // no stored credentials — the server requires them
		DefaultBranch:  "main",
	}

	// The first call genuinely attempts the network op and is rejected.
	_, err1 := cache.ListFiles(context.Background(), repo, "")
	require.Error(t, err1)
	require.ErrorIs(t, err1, gitcache.ErrGitAuthFailed)
	firstAttempts := requests.Load()
	require.Positive(t, firstAttempts, "the first call must have actually attempted the network operation")

	// A second call immediately after, still within the backoff window,
	// must NOT re-attempt the network op — bad/missing credentials will not
	// self-resolve via a wipe-and-reclone, so retrying on every request
	// just multiplies the cost of an unfixable failure.
	start := time.Now()
	_, err2 := cache.ListFiles(context.Background(), repo, "")
	elapsed := time.Since(start)
	require.Error(t, err2)
	require.ErrorIs(t, err2, gitcache.ErrGitAuthFailed)
	require.Lessf(t, elapsed, 100*time.Millisecond,
		"a repeat request within the backoff window took %s — it should fail instantly from the cached failure instead of re-attempting the network op", elapsed)
	require.Equal(t, firstAttempts, requests.Load(), "no additional network attempt should have been made while backed off")

	// After the backoff window expires, the next call is free to try
	// again (e.g. because an operator may have just fixed the credentials).
	time.Sleep(250 * time.Millisecond)
	_, err3 := cache.ListFiles(context.Background(), repo, "")
	require.Error(t, err3)
	require.Greater(t, requests.Load(), firstAttempts, "after the backoff window expires, the next call should retry the network op again")
}

// gitHTTPBackendPath locates the git-http-backend CGI binary that ships with
// any git installation (used by real servers like GitHub/GitLab/Gogs/Gitea to
// implement the smart-HTTP protocol). Skips the test if it can't be found,
// rather than failing on machines/CI images with an unusual git packaging.
func gitHTTPBackendPath(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skip("git --exec-path failed; cannot locate git-http-backend")
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, statErr := exec.LookPath(backend); statErr != nil {
		t.Skipf("git-http-backend not found/executable at %s", backend)
	}
	return backend
}

// newAuthRequiredGitServer starts a local HTTP server that serves bareDir
// over the real git smart-HTTP protocol (via git-http-backend, the same CGI
// program a real self-hosted git server like Gogs uses), but rejects every
// request lacking valid HTTP Basic credentials with a fast 401 — the
// well-behaved counterpart to newStalledGitServer above, used to prove that
// a server which DOES respond promptly with an auth rejection is correctly
// classified as ErrGitAuthFailed (not just bounded by the timeout).
func newAuthRequiredGitServer(t *testing.T, bareDir, wantUser, wantPass string) *httptest.Server {
	t.Helper()
	backend := gitHTTPBackendPath(t)
	handler := &cgi.Handler{
		Path: backend,
		Root: "/",
		Dir:  filepath.Dir(bareDir),
		Env: []string{
			"GIT_PROJECT_ROOT=" + filepath.Dir(bareDir),
			"GIT_HTTP_EXPORT_ALL=1",
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != wantUser || pass != wantPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestClone_MissingCredentialsFailsFastWithClearAuthError is the fast-fail
// counterpart to the stalled-remote tests above: some git servers (or the
// proxy/WAF in front of them) respond to bad/missing credentials with a
// prompt 401 rather than hanging. Even in that case, the failure must be
// classified and surfaced as "authentication failed for repo remote — check
// stored credentials", not a generic, opaque git error — this is what lets
// an operator immediately tell "my repo's stored credentials are wrong" from
// "the remote is unreachable" in the API error message.
func TestClone_MissingCredentialsFailsFastWithClearAuthError(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	srv := newAuthRequiredGitServer(t, bareURL, "validuser", "validpass")
	repoURL := srv.URL + "/remote.git"

	g := gitcache.NewGitRunner()
	cloneDir := t.TempDir()

	start := time.Now()
	err := g.Clone(cloneDir, repoURL, "", "main") // no credentials supplied
	elapsed := time.Since(start)

	require.Error(t, err, "clone against a repo requiring auth, with no stored credentials, must fail")
	require.ErrorIs(t, err, gitcache.ErrGitAuthFailed,
		"a fast 401 from the remote must be classified as an auth failure, not a generic git error")
	require.Less(t, elapsed, 5*time.Second, "a server that responds promptly with 401 should fail fast, not be held up by any client-side timeout")
}

// TestClone_WrongPasswordFailsFastWithClearAuthError covers the case
// relevant to repo 494ca9da if it turns out to have stored-but-incorrect
// credentials (as opposed to none at all): a wrong username/password embedded
// in the URL must be classified identically to missing credentials.
func TestClone_WrongPasswordFailsFastWithClearAuthError(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	srv := newAuthRequiredGitServer(t, bareURL, "validuser", "validpass")
	repoURL := srv.URL + "/remote.git"

	g := gitcache.NewGitRunner()
	cloneDir := t.TempDir()

	credJSON := `{"username":"validuser","password":"wrong-password"}`
	err := g.Clone(cloneDir, repoURL, credJSON, "main")

	require.Error(t, err)
	require.ErrorIs(t, err, gitcache.ErrGitAuthFailed)
}

// TestClone_CorrectCredentialsSucceedsAgainstAuthGatedRemote confirms the
// auth-gating test server (and the classification logic) doesn't produce
// false positives: valid stored credentials must still clone successfully.
func TestClone_CorrectCredentialsSucceedsAgainstAuthGatedRemote(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	srv := newAuthRequiredGitServer(t, bareURL, "validuser", "validpass")
	repoURL := srv.URL + "/remote.git"

	g := gitcache.NewGitRunner()
	cloneDir := t.TempDir()

	credJSON := `{"username":"validuser","password":"validpass"}`
	require.NoError(t, g.Clone(cloneDir, repoURL, credJSON, "main"))

	files, err := g.ListFiles(cloneDir)
	require.NoError(t, err)
	require.Contains(t, files, "hello.md")
}
