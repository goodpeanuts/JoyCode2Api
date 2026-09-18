package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonPID_WriteRead(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "test.pid")

	original := daemonPID{
		PID:       12345,
		Port:      34891,
		StartedAt: time.Now().Format(time.RFC3339),
	}

	b, _ := json.MarshalIndent(original, "", "  ")
	if err := os.WriteFile(pidFile, b, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	var loaded daemonPID
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if loaded.PID != original.PID {
		t.Errorf("PID = %d, want %d", loaded.PID, original.PID)
	}
	if loaded.Port != original.Port {
		t.Errorf("Port = %d, want %d", loaded.Port, original.Port)
	}
}

func TestCheckRunningDaemon_NoPIDFile(t *testing.T) {
	oldPIDFile := daemonPIDFile
	daemonPIDFile = filepath.Join(t.TempDir(), "nonexistent.pid")
	defer func() { daemonPIDFile = oldPIDFile }()

	pid, running := checkRunningDaemon()
	if running {
		t.Errorf("expected not running, got PID %d running=true", pid)
	}
}

func TestCheckRunningDaemon_StalePIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "stale.pid")

	data := daemonPID{PID: 999999998, Port: 34891, StartedAt: time.Now().Format(time.RFC3339)}
	b, _ := json.Marshal(data)
	os.WriteFile(pidFile, b, 0644)

	oldPIDFile := daemonPIDFile
	daemonPIDFile = pidFile
	defer func() { daemonPIDFile = oldPIDFile }()

	pid, running := checkRunningDaemon()
	if running {
		t.Errorf("expected stale PID to be detected, got PID %d running=true", pid)
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input  string
		expect int
	}{
		{"line1\nline2\nline3", 3},
		{"", 0},
		{"single", 1},
		{"a\nb\n", 2},
	}
	for _, tt := range tests {
		got := splitLines(tt.input)
		if len(got) != tt.expect {
			t.Errorf("splitLines(%q) = %d lines, want %d", tt.input, len(got), tt.expect)
		}
	}
}

func TestContainsStr(t *testing.T) {
	if !containsStr("hello world", "world") {
		t.Error("expected true for 'world' in 'hello world'")
	}
	if containsStr("hello", "world") {
		t.Error("expected false for 'world' in 'hello'")
	}
}

func TestMinDuration(t *testing.T) {
	if minDuration(1*time.Second, 2*time.Second) != 1*time.Second {
		t.Error("min(1s, 2s) should be 1s")
	}
	if minDuration(2*time.Second, 1*time.Second) != 1*time.Second {
		t.Error("min(2s, 1s) should be 1s")
	}
}

func TestTailLines(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	if got := tailLines(lines, 2); len(got) != 2 || got[0] != "d" || got[1] != "e" {
		t.Errorf("tailLines(lines,2) = %v, want [d e]", got)
	}
	if got := tailLines(lines, 10); len(got) != 5 {
		t.Errorf("tailLines(lines,10) len = %d, want 5", len(got))
	}
	if got := tailLines(lines, 0); got != nil {
		t.Errorf("tailLines(lines,0) = %v, want nil", got)
	}
	if got := tailLines(nil, 3); got != nil {
		t.Errorf("tailLines(nil,3) = %v, want nil", got)
	}
}

func TestDaemonProcessMatches_CurrentProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("cannot resolve test binary path")
	}
	// The recorded exe is this very test binary → ps comm must match.
	if !daemonProcessMatches(daemonPID{PID: os.Getpid(), Exe: exe}) {
		t.Errorf("daemonProcessMatches with own exe should be true (exe=%s)", exe)
	}
	// A made-up unrelated exe must not match (requires ps, present on darwin/linux test hosts).
	if daemonProcessMatches(daemonPID{PID: os.Getpid(), Exe: "/definitely/not/jcproxy-zzz"}) {
		t.Errorf("daemonProcessMatches with unrelated exe should be false")
	}
}

func TestServiceConfig_RoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeServiceConfig(serviceConfig{Port: 9999, SkipValidation: true})
	got, ok := readServiceConfig()
	if !ok || got.Port != 9999 || !got.SkipValidation {
		t.Fatalf("readServiceConfig() = %+v, ok=%v; want port 9999 skip=true", got, ok)
	}

	removeServiceConfig()
	if _, ok := readServiceConfig(); ok {
		t.Errorf("readServiceConfig after remove should miss")
	}
}

func TestInstalledServiceConfig_LegacyPlistFallback(t *testing.T) {
	if serviceUnitPath() == "" {
		t.Skip("no service unit path on this platform")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Legacy install without service.json: config recovered from plist text.
	path := serviceUnitPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	plist := `<plist><array><string>/usr/local/bin/jcproxy</string><string>serve</string>` +
		`<string>--port</string><string>8080</string></array></plist>`
	if err := os.WriteFile(path, []byte(plist), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, installed := installedServiceConfig()
	if !installed {
		t.Fatalf("installedServiceConfig should detect legacy plist install")
	}
	if cfg.Port != 8080 {
		t.Errorf("cfg.Port = %d, want 8080", cfg.Port)
	}
	if cfg.SkipValidation {
		t.Errorf("cfg.SkipValidation = true, want false (plist has no flag)")
	}
}
