// Package todoist files a message as a task. The TUI hands one task over per
// keypress; Todoist's REST API needs nothing else from larkim.
package todoist

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
)

// DefaultEndpoint is the tasks endpoint. It is exported so the configuration's
// default and this package name it in one place (mirrors jev.DefaultEndpoint).
const DefaultEndpoint = "https://api.todoist.com/api/v1/tasks"

// errBodyMax bounds how much of a refusal is quoted back. The reason is in
// the first line of it.
const errBodyMax = 2 << 10

// Client talks to the tasks endpoint.
type Client struct {
	token     string
	projectID string
	endpoint  string
	http      *http.Client
}

// New builds a client for an API token, putting every task in projectID (empty
// is Todoist's Inbox), against endpoint or DefaultEndpoint when that is empty.
// The endpoint parameter is not a config key; it is the seam the tests point
// at a local server, the way jev's is. Requests are bounded by the context
// they are given rather than by a client-wide timeout, because one caller's
// idea of too long is not another's.
func New(token, projectID, endpoint string) *Client {
	return &Client{token: token, projectID: projectID,
		endpoint: cmp.Or(endpoint, DefaultEndpoint), http: &http.Client{}}
}

// Task is one task. Only the fields this package sends or reads are named;
// the endpoint answers with more — due date, labels, the whole project tree —
// and none of it changes what a caller does.
type Task struct {
	Content     string `json:"content"`
	Description string `json:"description,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	ID          string `json:"id"`
}

// CreateTask posts one task and answers it as Todoist stored it, so the
// caller can name what was filed.
func (c *Client) CreateTask(ctx context.Context, t Task) (Task, error) {
	// The routing stays here: the one caller knows the message it is filing,
	// not the project the configuration picked.
	t.ProjectID = cmp.Or(t.ProjectID, c.projectID)
	b, err := json.Marshal(t)
	if err != nil {
		return Task{}, fmt.Errorf("todoist: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(b))
	if err != nil {
		return Task{}, fmt.Errorf("todoist: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Task{}, fmt.Errorf("todoist: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body carries the reason — a rejected token, a refused project —
		// and it is the only place it is said.
		why, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyMax))
		return Task{}, fmt.Errorf("todoist: %s: %s", resp.Status, bytes.TrimSpace(why))
	}
	var out Task
	if err := json.UnmarshalRead(resp.Body, &out); err != nil {
		return Task{}, fmt.Errorf("todoist: decode answer: %w", err)
	}
	return out, nil
}
