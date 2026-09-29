// Package jev asks TypeSafe's System One model which of a set of options fits
// a situation. One POST carries the state and every question about it, and the
// answers come back keyed by the names the questions were given: the model
// reads the state once and judges each question against that one reading, so
// asking two costs barely more than asking one.
package jev

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
)

// DefaultEndpoint is the evaluation endpoint. Every model is served by it and
// the request's own model field picks which one answers. It is exported so the
// configuration's default and this package name it in one place.
const DefaultEndpoint = "https://api.typesafe.ai/v1/systemone"

// model is the alias rather than a pinned version. An alias moves when a
// release ships, which is worth taking here: nothing downstream is calibrated
// against a particular version beyond one threshold the caller owns, and that
// threshold is a floor on a companion yes/no rather than a tuned cut.
const model = "jev-latest"

// errBodyMax bounds how much of a refusal is quoted back. The reason is in the
// first line of it; the rest is whatever the gateway felt like saying.
const errBodyMax = 2 << 10

// Client talks to the evaluation endpoint.
type Client struct {
	key      string
	endpoint string
	http     *http.Client
}

// New builds a client for an API key, against endpoint or DefaultEndpoint when
// that is empty. Requests are bounded by the context they are given rather than
// by a client-wide timeout, because one caller's idea of too long is not
// another's.
func New(key, endpoint string) *Client {
	return &Client{key: key, endpoint: cmp.Or(endpoint, DefaultEndpoint), http: &http.Client{}}
}

// Ask is one ranking question with a companion yes/no over the same state.
type Ask struct {
	// State is what both questions are answered against: a string, or any
	// value that marshals to a JSON object whose fields the questions name
	// between backticks.
	State any
	// Pick asks which option fits, and Options maps each option key to the
	// description it is judged by. The keys are the caller's own vocabulary
	// and come back unchanged in Rank.Options.
	Pick    string
	Options map[string]string
	// Fits asks whether the situation calls for any option at all, with True
	// and False saying what a yes and a no mean. An empty Fits skips the
	// question and leaves Rank.Fits at 1: a caller that did not ask has no
	// reason to be told the answer is no.
	Fits, True, False string
}

// Rank is one answer: the options best first, and how strongly the situation
// called for an option at all.
type Rank struct {
	Options []Option
	Fits    float64
}

// Option is one of the options the question offered and the probability the
// answer gave it. The probability travels with the key because the order alone
// cannot say where the answer stops being an answer and starts being the rest
// of the list: a caller that means to act on only the confident ones needs the
// number to cut at.
type Option struct {
	Key string
	P   float64
}

// Question ids. They name the answers in the response and are not sent to the
// model, so they carry no meaning it has to read.
const (
	pickID = "pick"
	fitsID = "fits"
)

// Rank answers a. The options come back sorted by probability, best first,
// with their keys breaking a tie so that two equal probabilities do not swap
// places between calls.
func (c *Client) Rank(ctx context.Context, a Ask) (Rank, error) {
	qs := map[string]question{
		pickID: {Type: "choice", Instructions: a.Pick, Criteria: a.Options},
	}
	if a.Fits != "" {
		qs[fitsID] = question{Type: "noul", Instructions: a.Fits,
			Criteria: map[string]string{"true": a.True, "false": a.False}}
	}
	var res response
	if err := c.post(ctx, request{State: a.State, Model: model, Questions: qs}, &res); err != nil {
		return Rank{}, err
	}
	pick, ok := res.Answers[pickID]
	if !ok {
		return Rank{}, fmt.Errorf("jev: no answer for %q", pickID)
	}
	out := Rank{Fits: 1}
	for _, key := range slices.SortedFunc(maps.Keys(pick.Probabilities), func(x, y string) int {
		return cmp.Or(cmp.Compare(pick.Probabilities[y], pick.Probabilities[x]), cmp.Compare(x, y))
	}) {
		out.Options = append(out.Options, Option{Key: key, P: pick.Probabilities[key]})
	}
	if fits, ok := res.Answers[fitsID]; ok {
		out.Fits = fits.Noul
	}
	return out, nil
}

// post sends one request and decodes the answer into out.
func (c *Client) post(ctx context.Context, body request, out *response) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("jev: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("jev: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("jev: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body carries the reason — a rejected key, a rate limit, a
		// question the endpoint refused — and it is the only place it is said.
		why, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyMax))
		return fmt.Errorf("jev: %s: %s", resp.Status, bytes.TrimSpace(why))
	}
	if err := json.UnmarshalRead(resp.Body, out); err != nil {
		return fmt.Errorf("jev: decode answer: %w", err)
	}
	return nil
}

// Wire shapes. Only the fields this package reads are named; the endpoint
// sends more — usage, the version that answered, each answer's own type — and
// none of it changes what a caller does with the answer.
type (
	request struct {
		State     any                 `json:"state"`
		Model     string              `json:"model"`
		Questions map[string]question `json:"questions"`
	}
	question struct {
		Type         string `json:"type"`
		Instructions string `json:"instructions"`
		// Criteria is a choice's option-to-description map or a noul's
		// true/false pair; the endpoint takes both under the one name.
		Criteria map[string]string `json:"criteria,omitempty"`
	}
	response struct {
		Answers map[string]answer `json:"answers"`
	}
	answer struct {
		// Probabilities is a choice's distribution over its own options.
		Probabilities map[string]float64 `json:"probabilities"`
		// Noul is a yes/no answer, from 0 for no to 1 for yes.
		Noul float64 `json:"noul"`
	}
)
