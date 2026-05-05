package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer() *server {
	return &server{
		state:   &installerState{status: "idle"},
		eventCh: make(chan string, 256),
		logBuf:  new(strings.Builder),
	}
}

func TestSysCheckEndpoint(t *testing.T) {
	srv := newTestServer()
	mux := srv.routes()

	req := httptest.NewRequest("GET", "/api/syscheck", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var sc SysCheck
	if err := json.NewDecoder(rr.Body).Decode(&sc); err != nil {
		t.Fatalf("decode syscheck: %v", err)
	}
	if sc.Arch == "" {
		t.Error("Arch should not be empty")
	}
}

func TestConfigEndpoint(t *testing.T) {
	srv := newTestServer()
	mux := srv.routes()

	body := `{"domain":"example.com","admin_email":"admin@example.com","setup_nginx":true,"setup_tls":true,"oidc_provider":"google","oidc_client_id":"cid","oidc_client_secret":"csec","secret_key":"aabbcc"}`
	req := httptest.NewRequest("POST", "/api/config", strings.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if srv.state.cfg == nil {
		t.Fatal("config not stored")
	}
	if srv.state.cfg.Domain != "example.com" {
		t.Errorf("expected domain example.com, got %s", srv.state.cfg.Domain)
	}
}

func TestRootServesHTML(t *testing.T) {
	srv := newTestServer()
	mux := srv.routes()

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<!DOCTYPE html>") {
		t.Error("expected HTML response")
	}
}
