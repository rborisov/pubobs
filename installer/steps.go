package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const repoDir = "/opt/pubobs"

func emit(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, event map[string]string) {
	data, _ := json.Marshal(event)
	ch <- string(data)
	mu.Lock()
	if text, ok := event["text"]; ok {
		logBuf.WriteString(text)
	}
	mu.Unlock()
}

func sectionStart(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, name string) {
	emit(ch, mu, logBuf, map[string]string{"type": "section_start", "name": name})
}

func sectionDone(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, name string) {
	emit(ch, mu, logBuf, map[string]string{"type": "section_done", "name": name})
}

func sectionError(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, name, message string) {
	b, _ := json.Marshal(map[string]string{"type": "section_error", "name": name, "message": message})
	ch <- string(b)
}

func done(ch chan string) {
	ch <- `{"type":"done"}`
}

func runCmd(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return err
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 512)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				emit(ch, mu, logBuf, map[string]string{"type": "log", "text": string(buf[:n])})
			}
			if err != nil {
				break
			}
		}
	}()

	err := cmd.Wait()
	pw.Close()
	wg.Wait()
	return err
}

func runInstall(cfg *installerConfig, ch chan string, logBuf *strings.Builder, mu *sync.Mutex) {
	sectionStart(ch, mu, logBuf, "Install Docker")
	if err := stepInstallDocker(ch, mu, logBuf); err != nil {
		sectionError(ch, mu, logBuf, "Install Docker", err.Error())
		done(ch)
		return
	}
	sectionDone(ch, mu, logBuf, "Install Docker")

	sectionStart(ch, mu, logBuf, "Build application")
	if err := stepBuildApp(ch, mu, logBuf); err != nil {
		sectionError(ch, mu, logBuf, "Build application", err.Error())
		done(ch)
		return
	}
	sectionDone(ch, mu, logBuf, "Build application")

	sectionStart(ch, mu, logBuf, "Start containers")
	if err := stepStartContainers(cfg, ch, mu, logBuf); err != nil {
		sectionError(ch, mu, logBuf, "Start containers", err.Error())
		done(ch)
		return
	}
	sectionDone(ch, mu, logBuf, "Start containers")

	if !cfg.SetupNginx {
		done(ch)
		return
	}

	sectionStart(ch, mu, logBuf, "Configure nginx")
	if err := stepConfigureNginx(cfg, ch, mu, logBuf); err != nil {
		sectionError(ch, mu, logBuf, "Configure nginx", err.Error())
		done(ch)
		return
	}
	sectionDone(ch, mu, logBuf, "Configure nginx")

	if !cfg.SetupTLS {
		done(ch)
		return
	}

	stepObtainTLS(cfg, ch, mu, logBuf)
}

func stepInstallDocker(ch chan string, mu *sync.Mutex, logBuf *strings.Builder) error {
	dockerOK := exec.Command("docker", "info").Run() == nil
	composeOK := exec.Command("docker", "compose", "version").Run() == nil

	if dockerOK && composeOK {
		emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Docker and Compose already installed, skipping.\n"})
		return nil
	}

	if !dockerOK {
		if err := runCmd(ch, mu, logBuf, "", "sh", "-c", "curl -fsSL https://get.docker.com | sh"); err != nil {
			return fmt.Errorf("docker install failed: %w", err)
		}
		if err := runCmd(ch, mu, logBuf, "", "systemctl", "enable", "--now", "docker"); err != nil {
			return err
		}
	}

	// Ensure Compose V2 plugin is present (may be missing on older Docker installs)
	if exec.Command("docker", "compose", "version").Run() != nil {
		if err := installComposePlugin(ch, mu, logBuf); err != nil {
			return err
		}
	}
	return nil
}

func installComposePlugin(ch chan string, mu *sync.Mutex, logBuf *strings.Builder) error {
	emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Installing Docker Compose V2 plugin...\n"})

	// Try apt — package name varies by distro/repo
	for _, pkg := range []string{"docker-compose-plugin", "docker-compose-v2"} {
		if exec.Command("apt-get", "install", "-y", pkg).Run() == nil {
			return nil
		}
	}

	// Fallback: download binary directly from GitHub releases
	emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "apt unavailable, downloading compose binary from GitHub...\n"})
	arch := "x86_64"
	if out, _ := exec.Command("uname", "-m").Output(); string(out) != "" {
		arch = strings.TrimSpace(string(out))
	}
	pluginDir := "/usr/local/lib/docker/cli-plugins"
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return fmt.Errorf("create plugin dir: %w", err)
	}
	dest := pluginDir + "/docker-compose"
	url := "https://github.com/docker/compose/releases/latest/download/docker-compose-linux-" + arch
	if err := runCmd(ch, mu, logBuf, "", "curl", "-fsSL", "-o", dest, url); err != nil {
		return fmt.Errorf("download compose: %w", err)
	}
	if err := os.Chmod(dest, 0755); err != nil {
		return fmt.Errorf("chmod compose: %w", err)
	}
	return nil
}

func stepBuildApp(ch chan string, mu *sync.Mutex, logBuf *strings.Builder) error {
	return runCmd(ch, mu, logBuf, repoDir+"/backend", "docker", "compose", "build")
}

func stepStartContainers(cfg *installerConfig, ch chan string, mu *sync.Mutex, logBuf *strings.Builder) error {
	envPath := repoDir + "/backend/.env"
	if err := writeEnvFile(envPath, cfg, cfg.SetupTLS); err != nil {
		return fmt.Errorf("write .env: %w", err)
	}
	emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Wrote " + envPath + "\n"})

	// Stop any existing containers first to free the port
	exec.Command("docker", "compose", "-f", repoDir+"/backend/docker-compose.yml", "down").Run()

	if err := runCmd(ch, mu, logBuf, repoDir+"/backend", "docker", "compose", "up", "-d"); err != nil {
		return err
	}
	return waitForHealthz(ch, mu, logBuf, "http://localhost:8080/healthz", 30*time.Second)
}

func waitForHealthz(ch chan string, mu *sync.Mutex, logBuf *strings.Builder, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Health check passed.\n"})
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Waiting for app to start...\n"})
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("app did not become healthy within %s", timeout)
}

func stepConfigureNginx(cfg *installerConfig, ch chan string, mu *sync.Mutex, logBuf *strings.Builder) error {
	if err := runCmd(ch, mu, logBuf, "", "apt-get", "install", "-y", "nginx"); err != nil {
		return err
	}
	nginxConf := fmt.Sprintf(`server {
    listen 80;
    server_name %s;
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        client_max_body_size 50m;
    }
}
`, cfg.Domain)
	confPath := "/etc/nginx/sites-available/pubobs"
	if err := os.WriteFile(confPath, []byte(nginxConf), 0644); err != nil {
		return fmt.Errorf("write nginx config: %w", err)
	}
	os.Symlink(confPath, "/etc/nginx/sites-enabled/pubobs")
	os.Remove("/etc/nginx/sites-enabled/default")
	if err := runCmd(ch, mu, logBuf, "", "nginx", "-t"); err != nil {
		return fmt.Errorf("nginx config test failed: %w", err)
	}
	return runCmd(ch, mu, logBuf, "", "systemctl", "reload", "nginx")
}

func stepObtainTLS(cfg *installerConfig, ch chan string, mu *sync.Mutex, logBuf *strings.Builder) {
	sectionStart(ch, mu, logBuf, "Obtain TLS certificate")

	if msg := checkDNS(cfg.Domain); msg != "" {
		b, _ := json.Marshal(map[string]string{
			"type":    "section_error",
			"name":    "Obtain TLS certificate",
			"message": msg,
		})
		ch <- string(b)
		// Do NOT emit done — stream stays open for Retry/Skip
		return
	}

	if err := runCmd(ch, mu, logBuf, "", "apt-get", "install", "-y", "certbot", "python3-certbot-nginx"); err != nil {
		sectionError(ch, mu, logBuf, "Obtain TLS certificate", err.Error())
		done(ch)
		return
	}
	if err := runCmd(ch, mu, logBuf, "", "certbot", "--nginx",
		"-d", cfg.Domain,
		"--non-interactive", "--agree-tos",
		"--register-unsafely-without-email",
	); err != nil {
		sectionError(ch, mu, logBuf, "Obtain TLS certificate", err.Error())
		// Stay open for retry
		return
	}
	sectionDone(ch, mu, logBuf, "Obtain TLS certificate")
	done(ch)
}

func retryTLS(cfg *installerConfig, ch chan string, logBuf *strings.Builder, mu *sync.Mutex) {
	stepObtainTLS(cfg, ch, mu, logBuf)
}

func skipTLS(cfg *installerConfig, ch chan string, logBuf *strings.Builder, mu *sync.Mutex) {
	envPath := repoDir + "/backend/.env"
	if err := writeEnvFile(envPath, cfg, false); err != nil {
		sectionError(ch, mu, logBuf, "Skip TLS", fmt.Sprintf("write .env: %v", err))
		done(ch)
		return
	}
	emit(ch, mu, logBuf, map[string]string{"type": "log", "text": "Updated .env to use http://\n"})
	exec.Command("docker", "compose", "-f", repoDir+"/backend/docker-compose.yml", "restart").Run()
	done(ch)
}

func checkDNS(domain string) string {
	addrs, err := net.LookupHost(domain)
	if err != nil || len(addrs) == 0 {
		return fmt.Sprintf("DNS lookup for %s failed: %v. Ensure your A record is set.", domain, err)
	}
	serverIP := publicIP()
	if serverIP == "" {
		return ""
	}
	for _, addr := range addrs {
		if addr == serverIP {
			return ""
		}
	}
	return fmt.Sprintf("DNS mismatch: %s resolves to %s but this server's IP is %s. Fix your DNS A record and retry, or skip TLS.", domain, addrs[0], serverIP)
}
