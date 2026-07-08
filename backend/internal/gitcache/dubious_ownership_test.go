package gitcache

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsDubiousOwnershipError guards the classification that IsHealthy
// relies on to tell git's "dubious ownership" rejection (a UID/config
// mismatch — self-healable by trusting the directory, never fixed by a
// re-clone) apart from genuine local-clone corruption (a dangling ref,
// missing objects, etc. — where a re-clone is exactly the right response).
// A real end-to-end reproduction of the dubious-ownership case needs a
// directory owned by a different UID than the test process, which
// generally requires root — this test instead pins down the pure
// string-matching logic against the actual message git prints (verified
// against git's own source/documentation for the "detected dubious
// ownership" fatal error introduced in git 2.35.2).
func TestIsDubiousOwnershipError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "real dubious ownership message",
			err:  errors.New("git rev-parse: exit status 128\nfatal: detected dubious ownership in repository at '/data/repos/494ca9da-2ac3-4124-b93e-d6551688f588'\nTo add an exception for this directory, call:\n\n\tgit config --global --add safe.directory /data/repos/494ca9da-2ac3-4124-b93e-d6551688f588\n"),
			want: true,
		},
		{
			name: "case-insensitive match",
			err:  errors.New("fatal: Detected Dubious Ownership in repository at '/x'"),
			want: true,
		},
		{
			name: "genuine corruption is not misclassified",
			err:  errors.New("git rev-parse: exit status 128\nfatal: bad object HEAD\n"),
			want: false,
		},
		{
			name: "dangling ref is not misclassified",
			err:  errors.New("git rev-parse: exit status 129\nfatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.\n"),
			want: false,
		},
		{
			name: "unrelated generic error",
			err:  errors.New("exec: \"git\": executable file not found in $PATH"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isDubiousOwnershipError(tc.err))
		})
	}
}
