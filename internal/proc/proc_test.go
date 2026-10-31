package proc

import (
	"errors"
	"testing"
	"time"
)

func TestRunIsBounded(t *testing.T) {
	cases := []struct {
		name    string
		script  string
		want    string
		timeout bool
	}{
		{name: "background child keeps the pipes open", script: "sleep 30 & echo ok", want: "ok\n"},
		{name: "hung command is killed", script: "sleep 30", timeout: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			out, _, err := Cmd{Name: "sh", Args: []string{"-c", tc.script}, Limit: 300 * time.Millisecond}.Run()
			if tc.timeout != errors.Is(err, ErrTimeout) || (!tc.timeout && err != nil) {
				t.Fatalf("err = %v", err)
			}
			if out != tc.want {
				t.Fatalf("out = %q", out)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("took %s", elapsed)
			}
		})
	}
}
