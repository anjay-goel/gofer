package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Host struct {
	Root     string `json:"root"`
	Terminal string `json:"terminal,omitempty"`
}

type Config struct {
	DefaultHost string          `json:"host"`
	Client      string          `json:"client"`
	Hosts       map[string]Host `json:"hosts"`
}

func Path() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "gofer", "config.json"), nil
}

func Load() (*Config, error) {
	cfg := &Config{Hosts: map[string]Host{}}
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.Hosts == nil {
		cfg.Hosts = map[string]Host{}
	}
	return cfg, nil
}

func (c *Config) Save() error {
	if c.Client == "" {
		b := make([]byte, 6)
		rand.Read(b)
		c.Client = hex.EncodeToString(b)
	}
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Requested(override string) string {
	if override != "" {
		return override
	}
	return os.Getenv("GOFER_HOST")
}

func (c *Config) Select(override string) (string, Host, error) {
	name := Requested(override)
	if name == "" {
		name = c.DefaultHost
	}
	if name == "" {
		return "", Host{}, errors.New("no dev box set up. Run gofer init")
	}
	host, ok := c.Hosts[name]
	if !ok {
		return "", Host{}, fmt.Errorf("%s isn't set up. Run gofer init %s", name, name)
	}
	return name, host, nil
}
