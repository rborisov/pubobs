package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

//go:embed static
var staticFiles embed.FS

type installerConfig struct {
	Domain             string `json:"domain"`
	AdminEmail         string `json:"admin_email"`
	SetupNginx         bool   `json:"setup_nginx"`
	SetupTLS           bool   `json:"setup_tls"`
	OIDCProvider       string `json:"oidc_provider"`
	OIDCIssuer         string `json:"oidc_issuer"`
	OIDCClientID       string `json:"oidc_client_id"`
	OIDCClientSecret   string `json:"oidc_client_secret"`
	YandexClientID     string `json:"yandex_client_id"`
	YandexClientSecret string `json:"yandex_client_secret"`
	SecretKey          string `json:"secret_key"`
}

type installerState struct {
	mu     sync.Mutex
	cfg    *installerConfig
	status string // idle | running | done | error
}

type server struct {
	state   *installerState
	eventCh chan string
	logBuf  *strings.Builder
	mu      sync.Mutex
}

func main() {
	port := flag.String("port", "8000", "port to listen on")
	flag.Parse()

	srv := &server{
		state:   &installerState{status: "idle"},
		eventCh: make(chan string, 1024),
		logBuf:  new(strings.Builder),
	}
	mux := srv.routes()
	addr := ":" + *port
	log.Printf("PubObs Installer listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("GET /api/syscheck", s.handleSysCheck)
	mux.HandleFunc("POST /api/config", s.handleConfig)
	mux.HandleFunc("POST /api/install", s.handleInstall)
	mux.HandleFunc("GET /api/install/stream", s.handleStream)
	mux.HandleFunc("POST /api/install/retry-tls", s.handleRetryTLS)
	mux.HandleFunc("POST /api/install/skip-tls", s.handleSkipTLS)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("POST /api/shutdown", s.handleShutdown)

	return mux
}

func (s *server) handleSysCheck(w http.ResponseWriter, r *http.Request) {
	sc := runSysCheck()
	writeJSON(w, sc)
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	var cfg installerConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if cfg.SecretKey == "" {
		key := make([]byte, 32)
		rand.Read(key)
		cfg.SecretKey = hex.EncodeToString(key)
	}
	s.state.mu.Lock()
	s.state.cfg = &cfg
	s.state.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) handleInstall(w http.ResponseWriter, r *http.Request) {
	s.state.mu.Lock()
	if s.state.status == "running" {
		s.state.mu.Unlock()
		http.Error(w, `{"error":"already running"}`, http.StatusConflict)
		return
	}
	s.state.status = "running"
	cfg := s.state.cfg
	s.state.mu.Unlock()

	if cfg == nil {
		http.Error(w, `{"error":"config not set"}`, http.StatusBadRequest)
		return
	}
	go runInstall(cfg, s.eventCh, s.logBuf, &s.mu)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	for {
		select {
		case event := <-s.eventCh:
			fmt.Fprintf(w, "data: %s\n\n", event)
			flusher.Flush()
			var e map[string]string
			json.Unmarshal([]byte(event), &e)
			if e["type"] == "done" {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) handleRetryTLS(w http.ResponseWriter, r *http.Request) {
	s.state.mu.Lock()
	cfg := s.state.cfg
	s.state.mu.Unlock()
	if cfg == nil {
		http.Error(w, `{"error":"no config"}`, http.StatusBadRequest)
		return
	}
	go retryTLS(cfg, s.eventCh, s.logBuf, &s.mu)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) handleSkipTLS(w http.ResponseWriter, r *http.Request) {
	s.state.mu.Lock()
	cfg := s.state.cfg
	s.state.mu.Unlock()
	if cfg != nil {
		go skipTLS(cfg, s.eventCh, s.logBuf, &s.mu)
	} else {
		s.eventCh <- `{"type":"done"}`
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) handleLogs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	logs := s.logBuf.String()
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", `attachment; filename="pubobs-install.log"`)
	fmt.Fprint(w, logs)
}

func (s *server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func generateSecretKey() string {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		log.Fatalf("generate key: %v", err)
	}
	return hex.EncodeToString(key)
}

func oidcIssuerForProvider(provider string) string {
	switch provider {
	case "google":
		return "https://accounts.google.com"
	default:
		return ""
	}
}

func baseURL(domain string, tls bool) string {
	if tls {
		return "https://" + domain
	}
	return "http://" + domain
}

func writeEnvFile(path string, cfg *installerConfig, useTLS bool) error {
	issuer := cfg.OIDCIssuer
	if issuer == "" {
		issuer = oidcIssuerForProvider(cfg.OIDCProvider)
	}
	lines := []string{
		"PUBOBS_BASE_URL=" + baseURL(cfg.Domain, useTLS),
		"PUBOBS_OIDC_ISSUER=" + issuer,
		"PUBOBS_OIDC_CLIENT_ID=" + cfg.OIDCClientID,
		"PUBOBS_OIDC_CLIENT_SECRET=" + cfg.OIDCClientSecret,
		"PUBOBS_SECRET_KEY=" + cfg.SecretKey,
		"PUBOBS_ADMIN_EMAIL=" + cfg.AdminEmail,
		"PUBOBS_YANDEX_CLIENT_ID=" + cfg.YandexClientID,
		"PUBOBS_YANDEX_CLIENT_SECRET=" + cfg.YandexClientSecret,
	}
	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0600)
}

// stubs replaced by steps.go in Task 8
func runInstall(cfg *installerConfig, ch chan string, logBuf *strings.Builder, mu *sync.Mutex) {}
func retryTLS(cfg *installerConfig, ch chan string, logBuf *strings.Builder, mu *sync.Mutex)  {}
func skipTLS(cfg *installerConfig, ch chan string, logBuf *strings.Builder, mu *sync.Mutex) {
	ch <- `{"type":"done"}`
}
