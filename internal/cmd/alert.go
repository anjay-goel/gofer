package cmd

import (
	"runtime"
	"time"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/proc"
)

const (
	alertClip  = 80
	alertLimit = 5 * time.Second
)

func alertText(v core.View) (title, body string) {
	title = "gofer: " + core.OneLine(v.Name)
	body = stateLabel(v, alertClip)
	if v.Next != "" {
		body += ". Run " + core.OneLine(v.Next)
	}
	return title, body
}

func alertCommand(goos, title, body string) (string, []string) {
	switch goos {
	case "darwin":
		script := "on run argv\ndisplay notification (item 2 of argv) with title (item 1 of argv)\nend run"
		return "osascript", []string{"-e", script, title, body}
	case "linux":
		return "notify-send", []string{"--", title, body}
	}
	return "", nil
}

func desktopAlert(title, body string) bool {
	name, args := alertCommand(runtime.GOOS, title, body)
	if name == "" {
		return false
	}
	_, _, err := proc.Cmd{Name: name, Args: args, Limit: alertLimit}.Run()
	return err == nil
}

func raiseAlerts(r core.WaitResult, inTerminal bool) {
	for _, v := range r.Views {
		if !v.EndsWait() {
			continue
		}
		title, body := alertText(v)
		if !desktopAlert(title, body) && inTerminal {
			terminalAlert(title + ": " + body)
		}
	}
}
