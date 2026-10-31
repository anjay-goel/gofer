package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

const (
	RPCPrefix           = "GOFER-RPC "
	CodeVersionMismatch = "version_mismatch"
)

type Request struct {
	Version string          `json:"version"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	Code   string          `json:"code,omitempty"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

func NewRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type Method[P, R any] string

const (
	Prepare   Method[PrepareParams, PrepareResult] = "prepare"
	Run       Method[RunParams, View]              = "run"
	List      Method[ListParams, []View]           = "ls"
	Wait      Method[WaitParams, WaitResult]       = "wait"
	Logs      Method[LogsParams, []LogLine]        = "logs"
	Send      Method[SendParams, SendResult]       = "send"
	Stop      Method[NameParams, StopResult]       = "stop"
	Remove    Method[RemoveParams, RemoveResult]   = "rm"
	Resolve   Method[NameParams, ResolveResult]    = "resolve"
	Doctor    Method[DoctorParams, []Check]        = "doctor"
	Linger    Method[struct{}, NoteResult]         = "linger"
	Uninstall Method[struct{}, UninstallResult]    = "uninstall"
)

type PrepareParams struct {
	Name string `json:"name"`
	Repo string `json:"repo"`
	Root string `json:"root"`
}

type PrepareResult struct {
	Dir string `json:"dir"`
}

type RunParams struct {
	RequestID string `json:"request_id"`
	Name      string `json:"name"`
	Repo      string `json:"repo"`
	Root      string `json:"root"`
	Prompt    string `json:"prompt"`
	Doc       string `json:"doc,omitempty"`
	DocName   string `json:"doc_name,omitempty"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Bypass    bool   `json:"bypass"`
	Base      string `json:"base,omitempty"`
}

type ListParams struct {
	Client string `json:"client"`
	Peek   bool   `json:"peek"`
}

type WaitParams struct {
	Names   []string `json:"names"`
	Timeout int      `json:"timeout"`
}

type WaitResult struct {
	Views       []View `json:"views"`
	Blocked     bool   `json:"blocked"`
	Finished    bool   `json:"finished"`
	Failed      bool   `json:"failed"`
	Unconfirmed bool   `json:"unconfirmed"`
	Trouble     string `json:"trouble,omitempty"`
}

func (r WaitResult) Done() bool { return r.Blocked || r.Finished }

type NameParams struct {
	Name string `json:"name"`
}

type StopResult struct {
	AlreadyStopped bool `json:"already_stopped"`
}

type LogsParams struct {
	Name  string `json:"name"`
	Limit int    `json:"limit"`
}

type SendParams struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

type SendResult struct {
	Action    string `json:"action"`
	Delivered bool   `json:"delivered"`
	Note      string `json:"note,omitempty"`
}

type RemoveParams struct {
	Name        string `json:"name"`
	Force       bool   `json:"force"`
	FetchedHead string `json:"fetched_head,omitempty"`
}

type RemoveResult struct {
	Unfetched string `json:"unfetched,omitempty"`
	Note      string `json:"note,omitempty"`
}

type ResolveResult struct {
	Job  string `json:"job"`
	Dir  string `json:"dir"`
	Note string `json:"note,omitempty"`
}

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type DoctorParams struct {
	Root string `json:"root"`
}

type NoteResult struct {
	Note string `json:"note"`
}

type UninstallResult struct {
	Removed []string `json:"removed"`
	Kept    []string `json:"kept"`
}
