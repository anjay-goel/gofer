package transport

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallRunsUnderAnyLoginShell(t *testing.T) {
	version := "1.0+it's"
	binary := []byte("#!/bin/sh\necho \"gofer 1.0+it's\"\n")
	for _, shell := range []string{"sh", "bash", "zsh", "fish", "tcsh"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skip("not installed")
			}
			home := t.TempDir()
			cmd := exec.Command(shell, "-c", Remote(installScript(version, len(binary))))
			cmd.Env = append(os.Environ(), "HOME="+home)
			cmd.Stdin = bytes.NewReader(binary)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if got := installedVersion(t, home); got != "gofer "+version {
				t.Fatalf("version = %s", got)
			}
		})
	}
}

func TestInstallScript(t *testing.T) {
	fake := func(version string) []byte { return []byte("#!/bin/sh\necho gofer " + version + "\n") }
	cases := []struct {
		name    string
		binary  []byte
		upload  int
		wantErr string
		want    string
	}{
		{name: "complete upload", binary: fake("2.0"), want: "2.0"},
		{name: "dropped upload", binary: fake("2.0"), upload: 5, wantErr: "cut short", want: "1.0"},
		{name: "binary that won't run", binary: []byte("#!/bin/false\n"), wantErr: "didn't run", want: "1.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".local", "share", "gofer")
			mustDo(t, os.MkdirAll(dir, 0o755))
			mustDo(t, os.WriteFile(filepath.Join(dir, "gofer"), fake("1.0"), 0o755))
			upload := tc.binary
			if tc.upload > 0 {
				upload = tc.binary[:tc.upload]
			}
			cmd := exec.Command("sh", "-c", installScript("2.0", len(tc.binary)))
			cmd.Env = append(os.Environ(), "HOME="+home)
			cmd.Stdin = bytes.NewReader(upload)
			out, err := cmd.CombinedOutput()
			if tc.wantErr == "" && err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if !strings.Contains(string(out), tc.wantErr) || (tc.wantErr != "" && err == nil) {
				t.Fatalf("err = %v, output %q, want %q", err, out, tc.wantErr)
			}
			if got := installedVersion(t, home); got != "gofer "+tc.want {
				t.Fatalf("version = %s", got)
			}
			leftovers, err := filepath.Glob(filepath.Join(dir, "gofer.*"))
			mustDo(t, err)
			if len(leftovers) != 0 {
				t.Fatalf("temp files left: %v", leftovers)
			}
		})
	}
}

func TestNetworkErrorRetryable(t *testing.T) {
	cases := []struct {
		detail string
		want   bool
	}{
		{"ssh: connect to host box port 22: Operation timed out", true},
		{"ssh: Could not resolve hostname box", true},
		{"you@box: Permission denied (publickey).", false},
		{"Host key verification failed.", false},
	}
	for _, tc := range cases {
		if got := (NetworkError{Detail: tc.detail}).Retryable(); got != tc.want {
			t.Errorf("%q: got %v", tc.detail, got)
		}
	}
}

func TestLocation(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"box", "box:/w"},
		{"u@box.tail", "u@box.tail:/w"},
		{"fd7a:115c::1", "[fd7a:115c::1]:/w"},
		{"u@fd7a:115c::1", "u@[fd7a:115c::1]:/w"},
	}
	for _, tc := range cases {
		if got := (&Client{Host: tc.host}).Location("/w"); got != tc.want {
			t.Errorf("%q: got %q", tc.host, got)
		}
	}
}

func TestParseUname(t *testing.T) {
	cases := []struct {
		out     string
		wantOS  string
		wantErr bool
	}{
		{out: "Linux x86_64", wantOS: "linux"},
		{out: "Welcome to the box\nLinux x86_64", wantOS: "linux"},
		{out: "nonsense here now", wantErr: true},
	}
	for _, tc := range cases {
		goos, arch, err := parseUname(tc.out)
		if (err != nil) != tc.wantErr || goos != tc.wantOS || (!tc.wantErr && arch != "amd64") {
			t.Errorf("%q: got %s %s, %v", tc.out, goos, arch, err)
		}
	}
}

func installedVersion(t *testing.T, home string) string {
	t.Helper()
	out, err := exec.Command(filepath.Join(home, ".local", "share", "gofer", "gofer"), "version").Output()
	mustDo(t, err)
	return strings.TrimSpace(string(out))
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
