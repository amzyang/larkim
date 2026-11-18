package automation

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/amzyang/larkim/store"
)

type Event struct {
	Version int           `json:"version"`
	JobID   int64         `json:"job_id"`
	RuleID  string        `json:"rule_id"`
	Message store.Message `json:"message"`
	Text    string        `json:"text"`
}

type Outcome struct {
	Status string `json:"status"`
	Result string `json:"result"`
}

type Executor interface {
	Execute(context.Context, Callback, Event) Outcome
}

type Exec struct{}

func (Exec) Execute(ctx context.Context, cb Callback, event Event) Outcome {
	input, err := json.Marshal(event)
	if err != nil {
		return Outcome{Status: "failed", Result: fmt.Sprintf("encode callback input: %v", err)}
	}
	ctx, cancel := context.WithTimeout(ctx, cb.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cb.Executable, cb.Args...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// Stop local descendants before handing the desktop to another lock owner.
	// A separately running GUI daemon can still finish an already submitted action.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return Outcome{Status: "failed", Result: fmt.Sprintf("start callback: %v", err)}
	}
	if err := cmd.Wait(); err != nil {
		return Outcome{Status: "unknown", Result: fmt.Sprintf("callback: %v; stdout=%s; stderr=%s", err, stdout.String(), stderr.String())}
	}
	var out Outcome
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return Outcome{Status: "unknown", Result: fmt.Sprintf("callback output: %v; stdout=%s; stderr=%s", err, stdout.String(), stderr.String())}
	}
	switch out.Status {
	case "succeeded", "failed", "unknown":
	default:
		return Outcome{Status: "unknown", Result: fmt.Sprintf("invalid callback status %q; stdout=%s; stderr=%s", out.Status, stdout.String(), stderr.String())}
	}
	if stderr.Len() > 0 {
		out.Result += "\nstderr: " + stderr.String()
	}
	return out
}
