package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch string
		want         string
		ok           bool
	}{
		{"darwin", "arm64", "joycode-proxy-darwin-arm64", true},
		{"linux", "amd64", "joycode-proxy-linux-amd64", true},
		{"darwin", "amd64", "", false},
		{"linux", "arm64", "", false},
		{"windows", "amd64", "", false},
	}
	for _, tt := range tests {
		got, ok := assetName(tt.goos, tt.goarch)
		if ok != tt.ok || got != tt.want {
			t.Errorf("assetName(%q,%q) = (%q,%v), want (%q,%v)",
				tt.goos, tt.goarch, got, ok, tt.want, tt.ok)
		}
	}
}

func TestNeedsUpdate(t *testing.T) {
	tests := []struct {
		name            string
		current, latest string
		wantNewer       bool
		wantForce       bool
	}{
		{"strictly newer", "v0.5.0", "v0.5.1", true, false},
		{"newer no v prefix", "0.5.0", "0.5.1", true, false},
		{"equal", "v0.5.1", "v0.5.1", false, false},
		{"older latest (downgrade)", "v0.5.2", "v0.5.1", false, false},
		{"major bump", "v0.9.9", "v1.0.0", true, false},
		{"dev current needs force", "dev-2dca7570e969-dirty", "v0.5.1", false, true},
		{"empty current needs force", "", "v0.5.1", false, true},
		{"invalid latest", "v0.5.0", "not-a-version", false, false},
		{"prerelease older than release", "v1.0.0-rc1", "v1.0.0", true, false},
		{"release newer than prerelease latest", "v1.0.0", "v1.0.0-rc1", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newer, force := needsUpdate(tt.current, tt.latest)
			if newer != tt.wantNewer || force != tt.wantForce {
				t.Errorf("needsUpdate(%q,%q) = (newer=%v, force=%v), want (newer=%v, force=%v)",
					tt.current, tt.latest, newer, force, tt.wantNewer, tt.wantForce)
			}
		})
	}
}

func TestParsePortAfterFlag(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
		ok   bool
	}{
		{"systemd unit", "ExecStart=/usr/local/bin/jcproxy serve --port 8080", 8080, true},
		{"plist xml", "<string>--port</string>\n        <string>34891</string>", 34891, true},
		{"no port flag", "ExecStart=/usr/local/bin/jcproxy serve", 0, false},
		{"port flag no number", "serve --port", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parsePortAfterFlag(c.in)
			if ok != c.ok || got != c.want {
				t.Errorf("parsePortAfterFlag(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestEnsureV(t *testing.T) {
	cases := map[string]string{
		"0.5.1":  "v0.5.1",
		"v0.5.1": "v0.5.1",
		"":       "",
		" 1.2.3": "v1.2.3",
	}
	for in, want := range cases {
		if got := ensureV(in); got != want {
			t.Errorf("ensureV(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLatestTag(t *testing.T) {
	var gotUA, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.2.3"})
	}))
	defer srv.Close()

	old := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = old }()

	tag, err := latestTag("owner/name")
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v1.2.3" {
		t.Errorf("tag = %q, want v1.2.3", tag)
	}
	if gotUA == "" {
		t.Error("expected a User-Agent header on the GitHub request")
	}
	if gotPath != "/repos/owner/name/releases/latest" {
		t.Errorf("request path = %q, want /repos/owner/name/releases/latest", gotPath)
	}
}

func TestLatestTagHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	old := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = old }()

	if _, err := latestTag("owner/missing"); err == nil {
		t.Error("expected error on HTTP 404, got nil")
	}
}
