package main

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type SysCheck struct {
	OS            string  `json:"os"`
	Arch          string  `json:"arch"`
	DiskFreeGB    float64 `json:"disk_free_gb"`
	DiskOK        bool    `json:"disk_ok"`
	DockerPresent bool    `json:"docker_present"`
	GitPresent    bool    `json:"git_present"`
}

func runSysCheck() SysCheck {
	var sc SysCheck
	sc.OS = runtime.GOOS
	sc.Arch = runtime.GOARCH

	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err == nil {
		freeBytes := stat.Bavail * uint64(stat.Bsize)
		sc.DiskFreeGB = float64(freeBytes) / (1 << 30)
	}
	sc.DiskOK = sc.DiskFreeGB >= 5.0

	sc.DockerPresent = commandExists("docker")
	sc.GitPresent = commandExists("git")

	return sc
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func osInfo() string {
	out, err := exec.Command("sh", "-c", `grep PRETTY_NAME /etc/os-release | cut -d'"' -f2`).Output()
	if err != nil || len(out) == 0 {
		return runtime.GOOS
	}
	return strings.TrimSpace(string(out))
}

func publicIP() string {
	out, err := exec.Command("curl", "-s", "--max-time", "5", "https://api.ipify.org").Output()
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(string(out))
	if len(ip) < 7 || len(ip) > 45 {
		return ""
	}
	return ip
}

func diskFreeStr(gb float64) string {
	return strconv.FormatFloat(gb, 'f', 1, 64) + " GB"
}
