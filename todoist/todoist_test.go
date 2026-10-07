package todoist

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// serve stands in for the endpoint, handing every request to h and pointing a
// client at it.
func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return New("k", "proj_1", s.URL)
}

func TestCreateTask_SendsTheTokenContentAndDescription(t *testing.T) {
	var got Task
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &got))
		io.WriteString(w, `{"id":"6Xv","content":"ship it"}`)
	})
	_, err := c.CreateTask(t.Context(), Task{Content: "ship it",
		Description: "lark://applink.feishu.cn/client/chat/open?openChatId=oc_quiet"})
	require.NoError(t, err)
	require.Equal(t, "ship it", got.Content)
	require.Equal(t, "lark://applink.feishu.cn/client/chat/open?openChatId=oc_quiet", got.Description)
}

func TestCreateTask_RoutesTheTaskToTheConfiguredProject(t *testing.T) {
	var got Task
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.UnmarshalRead(r.Body, &got))
		io.WriteString(w, `{}`)
	})
	_, err := c.CreateTask(t.Context(), Task{Content: "ship it"})
	require.NoError(t, err)
	require.Equal(t, "proj_1", got.ProjectID)
}

func TestCreateTask_LeavesTheProjectOutWhenNoneIsConfigured(t *testing.T) {
	var raw map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.UnmarshalRead(r.Body, &raw))
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(s.Close)
	c := New("k", "", s.URL)
	_, err := c.CreateTask(t.Context(), Task{Content: "ship it"})
	require.NoError(t, err)
	require.NotContains(t, raw, "project_id", "no project means Todoist's Inbox, not an empty one")
}

func TestCreateTask_AnswersTheTaskTodoistStored(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"id":"6Xv","content":"ship it","description":"d"}`)
	})
	got, err := c.CreateTask(t.Context(), Task{Content: "ship it", Description: "d"})
	require.NoError(t, err)
	require.Equal(t, Task{ID: "6Xv", Content: "ship it", Description: "d"}, got)
}

func TestCreateTask_QuotesTheReasonARefusalGives(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"unauthorized"}`)
	})
	_, err := c.CreateTask(t.Context(), Task{Content: "ship it"})
	require.ErrorContains(t, err, "401")
	require.ErrorContains(t, err, "unauthorized")
}

func TestCreateTask_CarriesTheCallersDeadline(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := c.CreateTask(ctx, Task{Content: "ship it"})
	require.ErrorIs(t, err, context.Canceled)
}

func TestNew_FallsBackToTheDefaultBase(t *testing.T) {
	require.Equal(t, DefaultBase, New("k", "", "").base)
	require.Equal(t, "http://elsewhere", New("k", "", "http://elsewhere").base)
}

func TestCreateTask_PostsUnderTheBaseItWasGiven(t *testing.T) {
	var method, path string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		io.WriteString(w, `{}`)
	})
	_, err := c.CreateTask(t.Context(), Task{Content: "ship it"})
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, method)
	require.Equal(t, "/tasks", path)
}

func TestProjects_WalksEveryPage(t *testing.T) {
	var cursors []string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/projects", r.URL.Path)
		require.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		require.Equal(t, "200", r.URL.Query().Get("limit"))
		cursor := r.URL.Query().Get("cursor")
		cursors = append(cursors, cursor)
		if cursor == "" {
			io.WriteString(w, `{"results":[{"id":"p_inbox","name":"Inbox","inbox_project":true},`+
				`{"id":"p_work","name":"平台组","inbox_project":false}],"next_cursor":"c2"}`)
			return
		}
		io.WriteString(w, `{"results":[{"id":"p_home","name":"Home"}],"next_cursor":null}`)
	})
	got, err := c.Projects(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"", "c2"}, cursors)
	require.Equal(t, []Project{
		{ID: "p_inbox", Name: "Inbox", Inbox: true},
		{ID: "p_work", Name: "平台组"},
		{ID: "p_home", Name: "Home"},
	}, got)
}

func TestProjects_QuotesTheReasonARefusalGives(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"unauthorized"}`)
	})
	_, err := c.Projects(t.Context())
	require.ErrorContains(t, err, "401")
	require.ErrorContains(t, err, "unauthorized")
}

func TestProjects_CarriesTheCallersDeadline(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := c.Projects(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestInboxFirst_MovesTheInboxAheadKeepingTheRestInOrder(t *testing.T) {
	ps := []Project{{ID: "p_work"}, {ID: "p_inbox", Inbox: true}, {ID: "p_home"}}
	require.Equal(t, []Project{{ID: "p_inbox", Inbox: true}, {ID: "p_work"}, {ID: "p_home"}}, InboxFirst(ps))
}

func TestProjectValue_IsEmptyForTheInbox(t *testing.T) {
	require.Equal(t, "", Project{ID: "p_inbox", Inbox: true}.Value())
	require.Equal(t, "p_home", Project{ID: "p_home"}.Value())
}
