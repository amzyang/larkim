package cuacallback

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
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

// Open navigates once, then waits for Lingxi's titled main window to expose
// its chat. Window discovery is retried inside the timeout loop so startup
// races resolve without failing immediately. A window on another Space reports
// an empty AX tree and open(1) on a lark:// link does not switch Spaces, so
// an unreadable window is brought forward once. Lingxi's untitled helper window
// stays above the main one, so bring_to_front reports the exact window
// unverified even when activation worked; its error is kept and only surfaces
// if the chat stays unreadable.
func (c *CUA) Open(ctx context.Context, url string) (Window, error) {
	if err := c.OpenURL(ctx, url); err != nil {
		return Window{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var w Window
	found := false
	var activateErr error
	activated := false
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		if !found {
			var result struct {
				Windows []struct {
					Window
					Title string `json:"title"`
				} `json:"windows"`
			}
			if err := c.Transport.Call(ctx, "list_windows", map[string]any{"app": "Lingxi", "session": c.Session}, &result); err != nil {
				return Window{}, err
			}
			var main []Window
			for _, win := range result.Windows {
				if win.Title != "" {
					main = append(main, win.Window)
				}
			}
			if len(main) > 1 {
				return Window{}, fmt.Errorf("expected one titled Lingxi window, found %d", len(main))
			}
			if len(main) == 1 {
				w = main[0]
				found = true
			}
		}
		if found {
			state, err := c.State(ctx, w)
			if err != nil {
				return Window{}, err
			}
			if state.Healthy() && slices.ContainsFunc(state.Elements, func(e Element) bool {
				return e.Role == "AXWebArea" && e.Label == "messenger-chat"
			}) {
				return w, nil
			}
			if !activated {
				activated = true
				activateErr = c.Transport.Call(ctx, "bring_to_front", map[string]any{"pid": w.PID, "window_id": w.ID, "session": c.Session}, &struct{}{})
			}
		}
		select {
		case <-ctx.Done():
			return Window{}, errors.Join(fmt.Errorf("wait for Lingxi AX window: %w", ctx.Err()), activateErr)
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
