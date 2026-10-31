package config

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type Candidate struct {
	Host   string
	Source string
}

type sshConfig struct {
	home    string
	visited map[string]bool
	hosts   []string
}

func (c *sshConfig) read(path string) {
	if c.visited[path] {
		return
	}
	c.visited[path] = true
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		fields := strings.Fields(strings.ReplaceAll(strings.SplitN(scan.Text(), "#", 2)[0], "=", " "))
		if len(fields) < 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "include":
			c.include(fields[1:])
		case "host":
			c.add(fields[1:])
		}
	}
}

func (c *sshConfig) include(patterns []string) {
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(expandInclude(pattern, c.home))
		sort.Strings(matches)
		for _, m := range matches {
			c.read(m)
		}
	}
}

func (c *sshConfig) add(names []string) {
	for _, name := range names {
		name = strings.Trim(name, `"`)
		if !strings.ContainsAny(name, "*?!") && !slices.Contains(c.hosts, name) {
			c.hosts = append(c.hosts, name)
		}
	}
}

func sshHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	c := sshConfig{home: home, visited: map[string]bool{}}
	c.read(filepath.Join(home, ".ssh", "config"))
	return c.hosts
}

func expandInclude(pattern, home string) string {
	if strings.HasPrefix(pattern, "~/") {
		return filepath.Join(home, pattern[2:])
	}
	if !filepath.IsAbs(pattern) {
		return filepath.Join(home, ".ssh", pattern)
	}
	return pattern
}

func tailscaleBin() string {
	if path, err := exec.LookPath("tailscale"); err == nil {
		return path
	}
	mac := "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	if _, err := os.Stat(mac); err == nil {
		return mac
	}
	return ""
}

func tailscalePeers() []string {
	bin := tailscaleBin()
	if bin == "" {
		return nil
	}
	out, err := exec.Command(bin, "status", "--json").Output()
	if err != nil {
		return nil
	}
	var status struct {
		Peer map[string]struct {
			HostName string `json:"HostName"`
			DNSName  string `json:"DNSName"`
			Online   bool   `json:"Online"`
		} `json:"Peer"`
	}
	if json.Unmarshal(out, &status) != nil {
		return nil
	}
	var peers []string
	for _, p := range status.Peer {
		if !p.Online {
			continue
		}
		name := strings.TrimSuffix(p.DNSName, ".")
		if name == "" {
			name = p.HostName
		}
		peers = append(peers, name)
	}
	sort.Strings(peers)
	return peers
}

var gitHosts = []string{"github.com", "gitlab.com", "bitbucket.org", "codeberg.org", "ssh.dev.azure.com"}

func Candidates() []Candidate {
	var out []Candidate
	for _, h := range sshHosts() {
		if !slices.Contains(gitHosts, strings.ToLower(h)) {
			out = append(out, Candidate{Host: h, Source: "ssh config"})
		}
	}
	for _, h := range tailscalePeers() {
		out = append(out, Candidate{Host: h, Source: "tailscale"})
	}
	return out
}
