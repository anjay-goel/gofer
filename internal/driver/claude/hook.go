package claude

import (
	"encoding/json"
	"io"
	"os"
)

const (
	failureLimit = 500
	socketEnv    = "CLAUDE_CODE_MESSAGING_SOCKET"
)

type HookKind string

const (
	HookSessionStart HookKind = "session_start"
	HookTurnEnd      HookKind = "turn_end"
	HookBlocked      HookKind = "blocked"
	HookSessionEnd   HookKind = "session_end"
)

var dialogTools = []string{"AskUserQuestion", "EnterPlanMode"}

var hookKinds = map[string]HookKind{
	"SessionStart": HookSessionStart,
	"Stop":         HookTurnEnd,
	"StopFailure":  HookTurnEnd,
	"Notification": HookBlocked,
	"SessionEnd":   HookSessionEnd,
}

type Hook struct {
	Kind      HookKind
	SessionID string
	Socket    string
	Failure   string
	Message   string
	Pending   bool
}

func ParseHook(r io.Reader) (Hook, error) {
	var in struct {
		SessionID    string            `json:"session_id"`
		Event        string            `json:"hook_event_name"`
		Message      string            `json:"message"`
		Error        string            `json:"error"`
		ErrorDetails string            `json:"error_details"`
		Tasks        []json.RawMessage `json:"background_tasks"`
		Crons        []json.RawMessage `json:"session_crons"`
	}
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return Hook{}, err
	}
	hook := Hook{
		Kind: hookKinds[in.Event], SessionID: in.SessionID, Socket: os.Getenv(socketEnv),
		Message: in.Message, Pending: len(in.Tasks)+len(in.Crons) > 0,
	}
	if in.Event == "StopFailure" {
		hook.Failure = failure(in.Error, in.ErrorDetails)
	}
	return hook, nil
}

func failure(kind, details string) string {
	text := kind
	if text == "" {
		text = "the turn failed"
	}
	if details != "" {
		text += ": " + details
	}
	return clip(text, failureLimit)
}

func HookSettings(command string, bypass bool) ([]byte, error) {
	handler := []map[string]any{{"type": "command", "command": command, "timeout": 10}}
	group := []map[string]any{{"hooks": handler}}
	settings := map[string]any{
		"permissions": map[string]any{"deny": dialogTools},
		"hooks": map[string]any{
			"SessionStart": group,
			"Stop":         group,
			"StopFailure":  group,
			"SessionEnd":   group,
			"Notification": []map[string]any{{"matcher": "permission_prompt|elicitation_dialog", "hooks": handler}},
		},
	}
	if bypass {
		settings["crossSessionInbound"] = "accept"
	}
	return json.MarshalIndent(settings, "", "  ")
}
