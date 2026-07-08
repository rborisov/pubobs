// backend/internal/api/update.go
package api

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/config"
)

// Version identifies the currently-running build. It's a package-level var
// (rather than a constant) so it can be overwritten at build time via
// `-ldflags -X` (see backend/Makefile) — the default "dev" only applies to
// local `go build`/`go test` runs where no ldflag is supplied. The backend
// has no tagged-release versioning of its own (unlike the Obsidian plugin's
// independent semver), so the update mechanism below treats this as an
// opaque token: the short git commit SHA the binary was built from,
// compared against the latest commit SHA on the configured update branch.
var Version = "dev"

const deployedVersionFile = ".pubobs-version"

// ReadDeployedVersion returns the commit SHA written by install.sh or the
// in-app updater to UpdateInstallDir/.pubobs-version. That marker names the
// commit whose deployment files were last applied and is the same source of
// truth install.sh --update uses. When the marker is missing, fall back to
// the embedded build Version (the commit the binary was compiled from).
func ReadDeployedVersion(installDir string) string {
	data, err := os.ReadFile(filepath.Join(installDir, deployedVersionFile))
	if err != nil {
		return Version
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return Version
	}
	return v
}

// VersionsEqual reports whether two git commit identifiers refer to the
// same revision. Full 40-char SHAs are compared exactly; otherwise a shared
// prefix of at least 7 hex chars is treated as a match so abbreviated UI
// labels still compare equal to ls-remote's full SHA.
func VersionsEqual(a, b string) bool {
	a = strings.TrimSpace(strings.ToLower(a))
	b = strings.TrimSpace(strings.ToLower(b))
	if a == "" || b == "" {
		return a == b
	}
	if a == b {
		return true
	}
	min := len(a)
	if len(b) < min {
		min = len(b)
	}
	if min < 7 {
		return false
	}
	return a[:min] == b[:min]
}

// updateExecFunc runs an external command with a fixed argument list (never
// shell-interpolated) and returns its combined stdout+stderr, mirroring
// exec.Command(name, args...).CombinedOutput(). It exists as a named type so
// RunUpdateCmdFunc below can be swapped out in tests.
type updateExecFunc func(dir, name string, args ...string) ([]byte, error)

// RunUpdateCmdFunc executes the git/docker commands used while applying an
// update. It's a variable — following the same pattern as S3ValidateFunc in
// admin_storage.go — so tests can substitute a fake runner without needing
// real git/Docker access.
var RunUpdateCmdFunc updateExecFunc = defaultRunUpdateCmd

func defaultRunUpdateCmd(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.CombinedOutput()
}

// latestVersionFunc resolves the latest commit SHA for a branch on a remote.
type latestVersionFunc func(repoURL, branch string) (string, error)

// LatestRemoteVersionFunc checks the latest commit on the configured update
// branch via `git ls-remote` (no local clone needed just to check). It's a
// variable so tests can substitute a fake lookup without needing real
// network access.
var LatestRemoteVersionFunc latestVersionFunc = defaultLatestRemoteVersion

func defaultLatestRemoteVersion(repoURL, branch string) (string, error) {
	out, err := exec.Command("git", "ls-remote", repoURL, "refs/heads/"+branch).Output()
	if err != nil {
		return "", fmt.Errorf("check remote version: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("remote branch %s not found", branch)
	}
	return fields[0], nil
}

type updateState struct {
	Status          string    `json:"status"` // "idle" | "running" | "done" | "error"
	Current         string    `json:"current"`
	CurrentShort    string    `json:"current_short"`
	Latest          string    `json:"latest"`
	LatestShort     string    `json:"latest_short"`
	UpdateAvailable bool      `json:"update_available"`
	Maintenance     bool      `json:"maintenance"`
	Message         string    `json:"message,omitempty"`
	Log             string    `json:"log,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// UpdateManager drives the in-app self-update flow: Check() reports whether
// a newer commit exists on the configured branch, and Start() applies it in
// the background by doing a throwaway sparse clone into a temp dir — no
// persistent full source checkout is ever kept, matching install.sh's own
// `--update` path — copying just the 3 files the live deployment needs
// (Dockerfile, docker-compose.yml, and the prebuilt binary for the host
// architecture) over UpdateInstallDir, then running
// `docker compose up -d --build` to rebuild and restart. This gives admins
// a one-click alternative to SSHing in and running `bash install.sh update`
// by hand, using the exact same underlying approach.
type UpdateManager struct {
	mu    sync.Mutex
	cfg   *config.Config
	state updateState
	log   strings.Builder
}

// NewUpdateManager constructs an UpdateManager seeded with the deployed
// version marker when present (see ReadDeployedVersion).
func NewUpdateManager(cfg *config.Config) *UpdateManager {
	current := ReadDeployedVersion(cfg.UpdateInstallDir)
	return &UpdateManager{
		cfg: cfg,
		state: updateState{
			Status:       "idle",
			Current:      current,
			CurrentShort: shortVersion(current),
			UpdatedAt:    time.Now().UTC(),
		},
	}
}

// State returns a snapshot of the current update status, including the
// accumulated log of the most recent (or in-progress) run.
func (m *UpdateManager) State() updateState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.state
	st.Log = m.log.String()
	return st
}

// MaintenanceActive reports whether requests to path should be blocked
// because an update is currently being applied. A small set of paths stay
// reachable regardless: the update endpoints themselves (so the admin UI can
// keep polling status), auth (so sessions don't get invalidated mid-update),
// and the health check (so orchestrators/monitoring don't misread the brief
// maintenance window as a hard failure).
func (m *UpdateManager) MaintenanceActive(path string) bool {
	m.mu.Lock()
	active := m.state.Maintenance
	m.mu.Unlock()
	if !active {
		return false
	}
	if strings.HasPrefix(path, "/api/admin/update") || strings.HasPrefix(path, "/auth/") || path == "/healthz" {
		return false
	}
	return true
}

// Check queries the remote for the latest commit on the configured branch
// and updates the reported state accordingly. It does not touch Status
// while a run is already "running" (Start owns that transition).
func (m *UpdateManager) Check() updateState {
	latest, err := LatestRemoteVersionFunc(m.cfg.UpdateRepoURL, m.cfg.UpdateBranch)
	current := ReadDeployedVersion(m.cfg.UpdateInstallDir)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Current = current
	m.state.CurrentShort = shortVersion(current)
	m.state.Latest = latest
	m.state.LatestShort = shortVersion(latest)
	m.state.UpdateAvailable = latest != "" && !VersionsEqual(current, latest)
	m.state.UpdatedAt = time.Now().UTC()
	if err != nil {
		m.state.Status = "error"
		m.state.Message = err.Error()
	} else if m.state.Status != "running" {
		m.state.Status = "idle"
		m.state.Message = ""
	}
	st := m.state
	st.Log = m.log.String()
	return st
}

// Start kicks off an update run in the background. It returns an error
// immediately (without starting a second run) if one is already in
// progress — this single-flight guard is the only thing preventing two
// concurrent `docker compose up --build` invocations from racing each
// other on the same install dir.
func (m *UpdateManager) Start() error {
	m.mu.Lock()
	if m.state.Status == "running" {
		m.mu.Unlock()
		return fmt.Errorf("update already running")
	}
	m.state.Status = "running"
	m.state.Maintenance = true
	m.state.Message = "Checking latest version"
	m.state.UpdatedAt = time.Now().UTC()
	m.log.Reset()
	m.mu.Unlock()

	go m.run()
	return nil
}

func (m *UpdateManager) run() {
	latest, err := LatestRemoteVersionFunc(m.cfg.UpdateRepoURL, m.cfg.UpdateBranch)
	if err != nil {
		m.fail("Check latest version", err)
		return
	}
	deployed := ReadDeployedVersion(m.cfg.UpdateInstallDir)
	if VersionsEqual(latest, deployed) {
		m.finish("Already up to date")
		return
	}

	tmpDir, err := os.MkdirTemp("", "pubobs-update-*")
	if err != nil {
		m.fail("Download update", err)
		return
	}
	defer os.RemoveAll(tmpDir)

	m.setMessage("Downloading update")
	if err := m.runCmd("", "git", "clone", "--branch", m.cfg.UpdateBranch, "--filter=blob:none", "--sparse", m.cfg.UpdateRepoURL, tmpDir); err != nil {
		m.fail("Download update", err)
		return
	}

	arch, err := updateArch()
	if err != nil {
		m.fail("Apply update", err)
		return
	}
	if err := m.runCmd(tmpDir, "git", "sparse-checkout", "set",
		"--no-cone",
		"backend/Dockerfile",
		"backend/docker-compose.yml",
		"backend/bin/pubobs-linux-"+arch,
	); err != nil {
		m.fail("Download update", err)
		return
	}

	m.setMessage("Applying update files")
	if err := CopyUpdateFiles(tmpDir, m.cfg.UpdateInstallDir, arch); err != nil {
		m.fail("Apply update", err)
		return
	}

	m.setMessage("Rebuilding and restarting application")
	backendDir := filepath.Join(m.cfg.UpdateInstallDir, "backend")
	if err := m.runCmd(backendDir, "docker", "compose", "up", "-d", "--build"); err != nil {
		m.fail("Restart application", err)
		return
	}

	if err := os.WriteFile(filepath.Join(m.cfg.UpdateInstallDir, ".pubobs-version"), []byte(latest+"\n"), 0644); err != nil {
		m.appendLog("Could not write version marker: " + err.Error() + "\n")
	}
	m.finish("Update applied. The service is restarting.")
}

func (m *UpdateManager) runCmd(dir, name string, args ...string) error {
	m.appendLog("$ " + name + " " + strings.Join(args, " ") + "\n")
	out, err := RunUpdateCmdFunc(dir, name, args...)
	if len(out) > 0 {
		m.appendLog(string(out))
	}
	return err
}

func (m *UpdateManager) setMessage(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Message = msg
	m.state.UpdatedAt = time.Now().UTC()
}

func (m *UpdateManager) appendLog(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.log.WriteString(text)
	m.state.UpdatedAt = time.Now().UTC()
}

func (m *UpdateManager) fail(step string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Status = "error"
	m.state.Maintenance = false
	m.state.Message = step + ": " + err.Error()
	m.state.UpdatedAt = time.Now().UTC()
}

func (m *UpdateManager) finish(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Status = "done"
	m.state.Maintenance = false
	m.state.Message = msg
	m.state.UpdatedAt = time.Now().UTC()
}

func handleAdminUpdateCheck(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		writeJSON(w, http.StatusOK, deps.Update.Check())
	}
}

func handleAdminUpdateStart(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		if err := deps.Update.Start(); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, deps.Update.State())
	}
}

func handleAdminUpdateStatus(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		writeJSON(w, http.StatusOK, deps.Update.State())
	}
}

// CopyUpdateFiles copies the 3 files a running deployment needs (Dockerfile,
// docker-compose.yml, and the prebuilt binary for arch) from a freshly
// sparse-cloned src tree into the live dest install dir. Exported so tests
// can exercise the apply step directly against a fake source dir, without
// needing a real git clone.
func CopyUpdateFiles(src, dest, arch string) error {
	files := map[string]string{
		filepath.Join(src, "backend", "Dockerfile"):                filepath.Join(dest, "backend", "Dockerfile"),
		filepath.Join(src, "backend", "docker-compose.yml"):        filepath.Join(dest, "backend", "docker-compose.yml"),
		filepath.Join(src, "backend", "bin", "pubobs-linux-"+arch): filepath.Join(dest, "backend", "bin", "pubobs-linux-"+arch),
	}
	for from, to := range files {
		if err := copyFile(from, to); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string) error {
	input, err := os.ReadFile(from)
	if err != nil {
		return fmt.Errorf("read %s: %w", from, err)
	}
	info, err := os.Stat(from)
	if err != nil {
		return fmt.Errorf("stat %s: %w", from, err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(to), err)
	}
	tmp := to + ".tmp"
	if err := os.WriteFile(tmp, input, info.Mode()); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	return os.Rename(tmp, to)
}

func updateArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return runtime.GOARCH, nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
}

func shortVersion(v string) string {
	if v == "" {
		return "unknown"
	}
	if len(v) > 12 {
		return v[:12]
	}
	return v
}
