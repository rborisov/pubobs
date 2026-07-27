package gitcache

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// A push that the remote refused for permission reasons (Gitea/Gogs and
// GitHub rulesets both surface this as a declined pre-receive hook) must be
// classified as ErrGitPushRejected — NOT as a history conflict. These two
// failures need opposite remedies: a rejected push means "this credential
// can't write here", while a non-fast-forward means "the branch moved, retry".
// Conflating them (as a bare `strings.Contains(err, "rejected")` does) sends
// the user chasing a nonexistent merge conflict.
func TestClassifyPushError_permissionDenial(t *testing.T) {
	err := errors.New(`git push: exit status 1
remote: Gitea: User permission denied for writing.
To https://git.example.com/u/r.git
 ! [remote rejected] HEAD -> main (pre-receive hook declined)
error: failed to push some refs to 'https://git.example.com/u/r.git'`)

	got := classifyPushError(err)
	require.ErrorIs(t, got, ErrGitPushRejected)
	require.NotErrorIs(t, got, ErrGitNonFastForward)
}

func TestClassifyPushError_protectedBranch(t *testing.T) {
	err := errors.New(`git push: exit status 1
remote: error: GH006: Protected branch update failed for refs/heads/main.
 ! [remote rejected] HEAD -> main (protected branch hook declined)`)

	require.ErrorIs(t, classifyPushError(err), ErrGitPushRejected)
}

// A genuine non-fast-forward keeps its own classification so it can still be
// reported as a retryable conflict.
func TestClassifyPushError_nonFastForward(t *testing.T) {
	err := errors.New(`git push: exit status 1
To https://git.example.com/u/r.git
 ! [rejected]        HEAD -> main (non-fast-forward)
error: failed to push some refs`)

	got := classifyPushError(err)
	require.ErrorIs(t, got, ErrGitNonFastForward)
	require.NotErrorIs(t, got, ErrGitPushRejected)
}

// Servers configured with receive.denyNonFastForwards report a non-fast-forward
// as a *remote* rejection. The underlying cause is still divergence, so the
// non-fast-forward marker must win over the generic remote-rejected one.
func TestClassifyPushError_remoteRejectedNonFastForwardIsConflict(t *testing.T) {
	err := errors.New(` ! [remote rejected] HEAD -> main (non-fast-forward)`)
	require.ErrorIs(t, classifyPushError(err), ErrGitNonFastForward)
}

// An auth failure that never reached the ref-advertisement stage (no refs were
// rejected at all) keeps its existing ErrGitAuthFailed classification.
func TestClassifyPushError_authFailurePassesThrough(t *testing.T) {
	err := classifyGitError(errors.New(`git push: exit status 128
fatal: Authentication failed for 'https://git.example.com/u/r.git/'`))
	got := classifyPushError(err)
	require.ErrorIs(t, got, ErrGitAuthFailed)
	require.NotErrorIs(t, got, ErrGitNonFastForward)
}

// A failure that is BOTH an auth failure and a remote rejection must satisfy
// errors.Is for both sentinels. classifyPushError used to wrap the inner error
// with %v, which severed the chain and discarded the ErrGitAuthFailed identity
// — and Cache.recordGitOpResult keys the auth-failure backoff off exactly that
// errors.Is check, so losing it makes a doomed network operation re-run on
// every request instead of backing off.
func TestClassifyPushError_authFailureAndRejectionKeepsBothSentinels(t *testing.T) {
	err := classifyGitError(errors.New(`git push: exit status 128
remote: Gitea: User permission denied for writing.
fatal: Authentication failed for 'https://git.example.com/u/r.git/'
 ! [remote rejected] HEAD -> main (pre-receive hook declined)`))
	require.ErrorIs(t, err, ErrGitAuthFailed, "precondition: classifyGitError marks this as an auth failure")

	got := classifyPushError(err)
	require.ErrorIs(t, got, ErrGitPushRejected)
	require.ErrorIs(t, got, ErrGitAuthFailed)
}

// Same requirement for the non-fast-forward branch, which wraps with the same
// verb and so had the same defect.
func TestClassifyPushError_authFailureAndNonFastForwardKeepsBothSentinels(t *testing.T) {
	err := classifyGitError(errors.New(`git push: exit status 128
fatal: Authentication failed for 'https://git.example.com/u/r.git/'
 ! [rejected] HEAD -> main (non-fast-forward)`))
	require.ErrorIs(t, err, ErrGitAuthFailed)

	got := classifyPushError(err)
	require.ErrorIs(t, got, ErrGitNonFastForward)
	require.ErrorIs(t, got, ErrGitAuthFailed)
}

func TestClassifyPushError_unrelatedErrorUntouched(t *testing.T) {
	err := errors.New("git push: exit status 128\nfatal: unable to access: Could not resolve host")
	got := classifyPushError(err)
	require.NotErrorIs(t, got, ErrGitPushRejected)
	require.NotErrorIs(t, got, ErrGitNonFastForward)
	require.Equal(t, err, got)
}

func TestClassifyPushError_nil(t *testing.T) {
	require.NoError(t, classifyPushError(nil))
}

// PushRejectionReason surfaces the remote's own explanation — the only part of
// a multi-line git failure that tells the user what to fix.
func TestPushRejectionReason(t *testing.T) {
	err := errors.New(`git push: exit status 1
remote: Gitea: User permission denied for writing.
To https://git.example.com/u/r.git
 ! [remote rejected] HEAD -> main (pre-receive hook declined)`)
	require.Equal(t, "Gitea: User permission denied for writing. (pre-receive hook declined)", PushRejectionReason(err))
}

func TestPushRejectionReason_parentheticalOnly(t *testing.T) {
	err := errors.New(` ! [remote rejected] HEAD -> main (permission denied)`)
	require.Equal(t, "permission denied", PushRejectionReason(err))
}

func TestPushRejectionReason_noneFound(t *testing.T) {
	require.Equal(t, "", PushRejectionReason(errors.New("git push: exit status 128")))
}

// Credentials are embedded in the remote URL passed to git, so anything
// derived from git's output must never be handed back to a client verbatim.
func TestPushRejectionReason_redactsCredentials(t *testing.T) {
	err := errors.New(`remote: denied for https://bob:s3cr3t@git.example.com/u/r.git
 ! [remote rejected] HEAD -> main (denied)`)
	reason := PushRejectionReason(err)
	require.NotContains(t, reason, "s3cr3t")
	require.Contains(t, reason, "git.example.com")
}
