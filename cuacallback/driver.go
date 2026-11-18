package cuacallback

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"os/exec"
	"slices"
	"time"
)

type Transport interface {
	Call(context.Context, string, map[string]any, any) error
}

// CLI uses the existing local daemon; it never changes its approval policy.
type CLI struct{ Executable string }

func (c CLI) Call(ctx context.Context, tool string, args map[string]any, out any) error {
	input, err := json.Marshal(args)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, c.Executable, "call", tool)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cua %s: %w; stdout=%s; stderr=%s", tool, err, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("decode cua %s: %w", tool, err)
	}
	return nil
}

type CUA struct {
	Transport Transport
	Session   string
	OpenURL   func(context.Context, string) error
}

func (c *CUA) Open(ctx context.Context, url string) (Window, error) {
	if err := c.OpenURL(ctx, url); err != nil {
		return Window{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		var result struct {
			Windows []Window `json:"windows"`
		}
		if err := c.Transport.Call(ctx, "list_windows", map[string]any{"app": "Lingxi", "session": c.Session}, &result); err != nil {
			return Window{}, err
		}
		if len(result.Windows) > 1 {
			return Window{}, fmt.Errorf("expected one Lingxi window, found %d", len(result.Windows))
		}
		if len(result.Windows) == 1 {
			w := result.Windows[0]
			state, err := c.State(ctx, w)
			if err != nil {
				return Window{}, err
			}
			if !state.Truncated && !state.Degraded && state.DegradedReason == "" && slices.ContainsFunc(state.Elements, func(e Element) bool {
				return e.Role == "AXWebArea" && e.Label == "messenger-chat"
			}) {
				return w, nil
			}
		}
		select {
		case <-ctx.Done():
			return Window{}, fmt.Errorf("wait for Lingxi AX window: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

func (c *CUA) State(ctx context.Context, w Window) (State, error) {
	var state State
	err := c.Transport.Call(ctx, "get_window_state", map[string]any{"pid": w.PID, "window_id": w.ID, "session": c.Session, "tree_format": "elements", "include_screenshot": false, "max_elements": 5000, "max_depth": 30, "timeout_ms": 5000}, &state)
	return state, err
}

func (c *CUA) Click(ctx context.Context, w Window, token string) error {
	var result struct {
		Effect string `json:"effect"`
		Route  string `json:"route"`
	}
	if err := c.Transport.Call(ctx, "click", map[string]any{"pid": w.PID, "window_id": w.ID, "session": c.Session, "element_token": token, "delivery_mode": "foreground"}, &result); err != nil {
		return err
	}
	if result.Route != "accessibility" {
		return fmt.Errorf("CUA did not report accessibility delivery: %s", result.Effect)
	}
	return nil
}
