package server

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/anjay-goel/gofer/internal/core"
)

func (s *Server) Serve(in io.Reader, out io.Writer) {
	var req core.Request
	var resp core.Response
	data, err := io.ReadAll(in)
	if err == nil {
		err = json.Unmarshal(data, &req)
	}
	switch {
	case err != nil:
		resp.Error = err.Error()
	case req.Version != s.Version:
		resp.Code = core.CodeVersionMismatch
		resp.Error = fmt.Sprintf("dev box runs gofer %s, client is %s", s.Version, req.Version)
	default:
		result, err := s.dispatch(req)
		if err == nil && result != nil {
			resp.Result, err = json.Marshal(result)
		}
		if err != nil {
			resp.Error = err.Error()
		}
	}
	line, _ := json.Marshal(resp)
	fmt.Fprintf(out, "%s%s\n", core.RPCPrefix, line)
}

type route struct {
	method string
	call   func(json.RawMessage) (any, error)
}

func handle[P, R any](method core.Method[P, R], fn func(P) (R, error)) route {
	return route{string(method), func(raw json.RawMessage) (any, error) {
		var p P
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
		}
		return fn(p)
	}}
}

func (s *Server) routes() []route {
	return []route{
		handle(core.Prepare, s.prepare),
		handle(core.Run, s.run),
		handle(core.List, s.list),
		handle(core.Wait, s.wait),
		handle(core.Logs, s.logs),
		handle(core.Send, s.send),
		handle(core.Stop, s.stop),
		handle(core.Remove, s.remove),
		handle(core.Resolve, s.resolve),
		handle(core.Doctor, s.doctor),
		handle(core.Linger, s.linger),
		handle(core.RCInstall, s.rcInstall),
		handle(core.Uninstall, s.uninstall),
	}
}

func (s *Server) dispatch(req core.Request) (any, error) {
	for _, r := range s.routes() {
		if r.method == req.Method {
			return r.call(req.Params)
		}
	}
	return nil, fmt.Errorf("unknown method %q", req.Method)
}
