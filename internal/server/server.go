package server

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/anjay-goel/gofer/internal/driver/claude"
	"github.com/anjay-goel/gofer/internal/store"
)

type Server struct {
	Home    string
	Store   *store.Store
	Driver  *claude.Driver
	Version string
}

func New(version string) (*Server, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Server{Home: home, Store: store.New(home), Driver: claude.New(home), Version: version}, nil
}

func isHomeRelative(path string) bool {
	return path == "~" || strings.HasPrefix(path, "~/")
}

func (s *Server) expand(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if isHomeRelative(path) {
		path = strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/")
	}
	return filepath.Join(s.Home, path)
}

func (s *Server) repoDir(repo, root string) string {
	if filepath.IsAbs(repo) || isHomeRelative(repo) {
		return s.expand(repo)
	}
	return filepath.Join(s.expand(root), repo)
}
