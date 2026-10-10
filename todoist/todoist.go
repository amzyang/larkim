// Package todoist files a message as a task and lists the projects one can
// land in. The TUI hands one task over per keypress; Todoist's REST API needs
// nothing else from larkim.
package todoist

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"

	"github.com/amzyang/larkim/internal/oplog"
)

// DefaultBase is the API root every endpoint hangs off. It is exported so the
// configuration's default and this package name it in one place (mirrors
// jev.DefaultEndpoint).
const DefaultBase = "https://api.todoist.com/api/v1"

// errBodyMax bounds how much of a refusal is quoted back. The reason is in
// the first line of it.
const errBodyMax = 2 << 10

// pageLimit is the most projects one page carries; the endpoint refuses more.
const pageLimit = "200"

// Client talks to the API.
type Client struct {
	token     string
	projectID string
	base      string
	http      *http.Client
}

// New builds a client for an API token, putting every task in projectID (empty
// is Todoist's Inbox), against base or DefaultBase when that is empty. The
// base parameter is not a config key; it is the seam the tests point at a
// local server, the way jev's endpoint is. Requests are bounded by the context
// they are given rather than by a client-wide timeout, because one caller's
// idea of too long is not another's. Every request is logged to log; nil
// discards.
func New(token, projectID, base string, log *slog.Logger) *Client {
	return &Client{token: token, projectID: projectID,
		base: cmp.Or(base, DefaultBase), http: &http.Client{Transport: oplog.Transport{Log: log, Name: "todoist"}}}
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

// Project is one place a task can land. Inbox marks the one Todoist files a
// task without a project into.
type Project struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Inbox bool   `json:"inbox_project"`
}

// Value is the project id a task names to land in p. The Inbox is the empty
// value: a task that names no project is where Todoist files it.
func (p Project) Value() string {
	if p.Inbox {
		return ""
	}
	return p.ID
}

// InboxFirst moves the Inbox to the head of ps, the rest in the order Todoist
// keeps them: it is the default, and the value an empty setting stands for.
func InboxFirst(ps []Project) []Project {
	i := slices.IndexFunc(ps, func(p Project) bool { return p.Inbox })
	if i <= 0 {
		return ps
	}
	return slices.Concat(ps[i:i+1], ps[:i], ps[i+1:])
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
	var out Task
	if err := c.do(ctx, http.MethodPost, "/tasks", bytes.NewReader(b), &out); err != nil {
		return Task{}, err
	}
	return out, nil
}

// Projects lists every active project, in the order Todoist answers them,
// walking the pages until the endpoint names no further cursor.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var all []Project
	cursor := ""
	for {
		q := url.Values{"limit": {pageLimit}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Results    []Project `json:"results"`
			NextCursor string    `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, "/projects?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// do sends one request under the token and decodes a 200 into out.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("todoist: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("todoist: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body carries the reason — a rejected token, a refused project —
		// and it is the only place it is said.
		why, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyMax))
		return fmt.Errorf("todoist: %s: %s", resp.Status, bytes.TrimSpace(why))
	}
	if err := json.UnmarshalRead(resp.Body, out); err != nil {
		return fmt.Errorf("todoist: decode answer: %w", err)
	}
	return nil
}
