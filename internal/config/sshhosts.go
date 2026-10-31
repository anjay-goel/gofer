package config

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Candidate struct {
	Host   string
	Source string
}

func SSHHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	visited := map[string]bool{}
	var hosts []string
	var read func(string)
	read = func(path string) {
		if visited[path] {
			return
		}
		visited[path] = true
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
				for _, pattern := range fields[1:] {
					pattern = expandInclude(pattern, home)
					matches, _ := filepath.Glob(pattern)
					sort.Strings(matches)
					for _, m := range matches {
						read(m)
					}
				}
			case "host":
				for _, h := range fields[1:] {
					if strings.ContainsAny(h, "*?!") || seen[h] {
						continue
					}
					seen[h] = true
					hosts = append(hosts, strings.Trim(h, `"`))
				}
			}
		}
	}
	read(filepath.Join(home, ".ssh", "config"))
	return hosts
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

func TailscalePeers() []string {
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

func Candidates() []Candidate {
	var out []Candidate
	for _, h := range SSHHosts() {
		out = append(out, Candidate{Host: h, Source: "ssh config"})
	}
	for _, h := range TailscalePeers() {
		out = append(out, Candidate{Host: h, Source: "tailscale"})
	}
	return out
}
