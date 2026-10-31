package claude

import (
	"encoding/json"
	"strings"
)

const (
	RemoteEnv        = `PATH="$HOME/.local/bin:$PATH"`
	InstallScript    = "curl -fsSL https://claude.ai/install.sh | bash"
	ProbeScript      = `command -v claude >/dev/null 2>&1 || test -x "$HOME/.local/bin/claude"`
	LoginScript      = RemoteEnv + " claude auth login"
	AuthStatusScript = RemoteEnv + " claude auth status --json"
)

func AttachScript(job string) string {
	return RemoteEnv + " exec claude attach " + job
}

func ParseAuth(stdout string) (loggedIn bool, method string, ok bool) {
	var status struct {
		LoggedIn bool   `json:"loggedIn"`
		Method   string `json:"authMethod"`
	}
	start := strings.Index(stdout, "{")
	if start < 0 || json.Unmarshal([]byte(stdout[start:]), &status) != nil {
		return false, "", false
	}
	return status.LoggedIn, status.Method, true
}
