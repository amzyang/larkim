// Package automation evaluates current message state and dispatches durable callbacks.
package automation

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/store"
)

type Rule struct {
	ID       string   `yaml:"id" json:"id"`
	Enabled  bool     `yaml:"enabled" json:"enabled"`
	Criteria Criteria `yaml:"criteria" json:"criteria"`
	Callback Callback `yaml:"callback" json:"callback"`
}

type Criteria struct {
	ChatIDs      []string `yaml:"chat_ids" json:"chat_ids"`
	SenderIDs    []string `yaml:"sender_ids" json:"sender_ids"`
	MessageTypes []string `yaml:"message_types" json:"message_types"`
	Contains     []string `yaml:"contains" json:"contains"`
	Excludes     []string `yaml:"excludes" json:"excludes"`
}

type Callback struct {
	Executable string        `yaml:"executable" json:"executable"`
	Args       []string      `yaml:"args" json:"args"`
	Timeout    time.Duration `yaml:"timeout" json:"timeout"`
}

func ValidateRules(rules []Rule) error {
	seen := map[string]bool{}
	for _, r := range rules {
		if strings.TrimSpace(r.ID) == "" || seen[r.ID] {
			return fmt.Errorf("automation: rule id %q must be nonempty and unique", r.ID)
		}
		seen[r.ID] = true
		if strings.TrimSpace(r.Callback.Executable) == "" || r.Callback.Timeout <= 0 {
			return fmt.Errorf("automation %s: callback requires executable and positive timeout", r.ID)
		}
		c := r.Criteria
		if len(c.ChatIDs)+len(c.SenderIDs)+len(c.MessageTypes)+len(c.Contains)+len(c.Excludes) == 0 {
			return fmt.Errorf("automation %s: criteria must not be empty", r.ID)
		}
		for _, values := range [][]string{c.ChatIDs, c.SenderIDs, c.MessageTypes, c.Contains, c.Excludes} {
			for _, value := range values {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("automation %s: criteria values must not be empty", r.ID)
				}
			}
		}
	}
	return nil
}

func (c Criteria) Match(m store.Message) (string, bool) {
	if m.Deleted || m.RenderedAt == 0 {
		return "", false
	}
	if len(c.ChatIDs) > 0 && !slices.Contains(c.ChatIDs, m.ChatID) ||
		len(c.SenderIDs) > 0 && !slices.Contains(c.SenderIDs, m.SenderID) ||
		len(c.MessageTypes) > 0 && !slices.Contains(c.MessageTypes, m.MsgType) {
		return "", false
	}
	text, raw := card.MessageText(m.ContentRaw, m.Content, true)
	if raw {
		return "", false
	}
	for _, term := range c.Contains {
		if !strings.Contains(text, term) {
			return text, false
		}
	}
	for _, term := range c.Excludes {
		if strings.Contains(text, term) {
			return text, false
		}
	}
	return text, true
}
