package main

import (
	"testing"
)

func TestSysCheckFields(t *testing.T) {
	sc := runSysCheck()
	if sc.Arch == "" {
		t.Error("Arch should not be empty")
	}
	if sc.OS == "" {
		t.Error("OS should not be empty")
	}
	if sc.DiskFreeGB <= 0 {
		t.Errorf("expected DiskFreeGB > 0, got %f", sc.DiskFreeGB)
	}
}
