package jev

import (
	"context"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amzyang/larkim/internal/oplog"
	"github.com/stretchr/testify/require"
)

// serve stands in for the endpoint, handing every request to h and pointing a
// client at it.
func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	return serveLogged(t, h, nil)
}

func serveLogged(t *testing.T, h http.HandlerFunc, log *slog.Logger) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return New("k", s.URL, log)
}

// ask is the shape every case here sends.
var ask = Ask{
	State:   map[string]string{"target": "shipped it"},
	Pick:    "which emoji fits?",
	Options: map[string]string{"ROCKET": "shipped", "BUG": "a defect"},
	Fits:    "is this worth reacting to?",
	True:    "an announcement",
	False:   "a question",
}

func TestRank_SendsTheStateAndBothQuestions(t *testing.T) {
	var got request
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &got))
		io.WriteString(w, `{"answers":{"pick":{"probabilities":{"ROCKET":0.9,"BUG":0.1}},"fits":{"noul":0.8}}}`)
	})
	_, err := c.Rank(t.Context(), ask)
	require.NoError(t, err)
	require.Equal(t, model, got.Model)
	require.Equal(t, map[string]any{"target": "shipped it"}, got.State)
	require.Equal(t, "choice", got.Questions[pickID].Type)
	require.Equal(t, ask.Options, got.Questions[pickID].Criteria)
	require.Equal(t, "noul", got.Questions[fitsID].Type)
	require.Equal(t, map[string]string{"true": "an announcement", "false": "a question"},
		got.Questions[fitsID].Criteria)
}

func TestRank_AsksEveryNamedNoulInTheSameRequest(t *testing.T) {
	var got request
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &got))
		io.WriteString(w, `{"answers":{"pick":{"probabilities":{"ROCKET":1}},"fits":{"noul":0.8},"noul:mine":{"noul":0.3}}}`)
	})
	a := ask
	a.Nouls = map[string]Noul{"mine": {Instructions: "is this mine?", True: "it names me", False: "it names someone else"}}
	r, err := c.Rank(t.Context(), a)
	require.NoError(t, err)
	require.Len(t, got.Questions, 3)
	q := got.Questions["noul:mine"]
	require.Equal(t, "noul", q.Type)
	require.Equal(t, "is this mine?", q.Instructions)
	require.Equal(t, map[string]string{"true": "it names me", "false": "it names someone else"}, q.Criteria)
	require.Equal(t, map[string]float64{"mine": 0.3}, r.Nouls)
	require.InDelta(t, 0.8, r.Fits, 1e-9)
}

func TestRank_ANamedNoulCannotTakeTheCompanionsPlace(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"answers":{"pick":{"probabilities":{"ROCKET":1}},"fits":{"noul":0.8},"noul:fits":{"noul":0.1}}}`)
	})
	a := ask
	a.Nouls = map[string]Noul{"fits": {Instructions: "x", True: "y", False: "z"}}
	r, err := c.Rank(t.Context(), a)
	require.NoError(t, err)
	require.InDelta(t, 0.8, r.Fits, 1e-9)
	require.Equal(t, map[string]float64{"fits": 0.1}, r.Nouls)
}

func TestRank_AnAnswerMissingANamedNoulIsAnError(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"answers":{"pick":{"probabilities":{"ROCKET":1}},"fits":{"noul":0.8}}}`)
	})
	a := ask
	a.Nouls = map[string]Noul{"mine": {Instructions: "x", True: "y", False: "z"}}
	_, err := c.Rank(t.Context(), a)
	require.ErrorContains(t, err, "mine")
}

func TestRank_OrdersTheOptionsByProbability(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"answers":{"pick":{"probabilities":{"BUG":0.1,"ROCKET":0.9,"OK":0.4}},"fits":{"noul":0.8}}}`)
	})
	r, err := c.Rank(t.Context(), ask)
	require.NoError(t, err)
	require.Equal(t, []Option{{"ROCKET", 0.9}, {"OK", 0.4}, {"BUG", 0.1}}, r.Options)
	require.InDelta(t, 0.8, r.Fits, 1e-9)
}

func TestRank_BreaksATieByKeySoTwoCallsAgree(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"answers":{"pick":{"probabilities":{"OK":0.5,"BUG":0.5,"ROCKET":0.5}}}}`)
	})
	for range 3 {
		r, err := c.Rank(t.Context(), ask)
		require.NoError(t, err)
		require.Equal(t, []Option{{"BUG", 0.5}, {"OK", 0.5}, {"ROCKET", 0.5}}, r.Options)
	}
}

func TestRank_WithoutACompanionQuestionAsksOnlyTheChoice(t *testing.T) {
	var got request
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &got))
		io.WriteString(w, `{"answers":{"pick":{"probabilities":{"ROCKET":1}}}}`)
	})
	a := ask
	a.Fits = ""
	r, err := c.Rank(t.Context(), a)
	require.NoError(t, err)
	require.Len(t, got.Questions, 1)
	require.Equal(t, float64(1), r.Fits, "a caller that did not ask is not told no")
}

func TestRank_QuotesTheReasonARefusalGives(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"invalid api key"}`)
	})
	_, err := c.Rank(t.Context(), ask)
	require.ErrorContains(t, err, "401")
	require.ErrorContains(t, err, "invalid api key")
}

func TestRank_AnAnswerMissingTheChoiceIsAnError(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"answers":{"fits":{"noul":0.8}}}`)
	})
	_, err := c.Rank(t.Context(), ask)
	require.ErrorContains(t, err, "no answer")
}

func TestRank_CarriesTheCallersDeadline(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"answers":{"pick":{"probabilities":{"ROCKET":1}}}}`)
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := c.Rank(ctx, ask)
	require.ErrorIs(t, err, context.Canceled)
}

func TestNew_FallsBackToTheDefaultEndpoint(t *testing.T) {
	require.Equal(t, DefaultEndpoint, New("k", "", nil).endpoint)
	require.Equal(t, "http://x", New("k", "http://x", nil).endpoint)
}

func TestClient_LogsEveryCallUnderTheCallersOperation(t *testing.T) {
	var buf strings.Builder
	log := oplog.NewText(&buf, slog.LevelDebug)
	c := serveLogged(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}, log)
	ctx := oplog.With(t.Context(), "probe")
	op, _ := oplog.From(ctx)

	_, err := c.Rank(ctx, ask)
	require.Error(t, err)

	require.Contains(t, buf.String(), "name=jev")
	require.Contains(t, buf.String(), "op_id="+op.ID)
}
