package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/amzyang/larkim/triage"
)

// presenceDir is where every TUI of this data dir answers for itself.
func (a *App) presenceDir() string { return filepath.Join(a.cfg.DataDir, "tui") }

// aiDir is where the agent runs: a directory of its own with nothing in it,
// since the transcript is all it is meant to read.
func (a *App) aiDir() string { return filepath.Join(a.cfg.DataDir, "ai") }

func (a *App) makeAIDir() error { return os.MkdirAll(a.aiDir(), 0o700) }

// newAgent is the agent command line's client, nil when it names nothing on
// PATH.
func (a *App) newAgent(agent, model string) *ai.Client {
	argv := strings.Fields(agent)
	if len(argv) == 0 {
		return nil
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return nil
	}
	return ai.New(argv, model, a.aiDir(), a.logger())
}

// newJev is the Jev client keyed by the variable keyEnv names, nil when it is
// unset.
func (a *App) newJev(keyEnv, endpoint string) *jev.Client {
	key := os.Getenv(keyEnv)
	if key == "" {
		return nil
	}
	return jev.New(key, endpoint, a.logger())
}

// triager builds the banner side for the process that holds daemon.lock. Each
// remote half is left out when what it needs is missing — no Jev key, no
// agent on PATH, no alerter — and the rest runs without it.
func (a *App) triager(st *store.Store) (*triage.Triager, error) {
	t := &triage.Triager{Store: st, Clock: sync.RealClock{}, DataDir: a.cfg.DataDir, Log: a.logger(),
		Desktop: triage.MacDesktop{Log: a.logger()}, Presence: triage.Sockets{Dir: a.presenceDir()},
		Self: func(ctx context.Context) string { return selfOpenID(ctx, st) }}
	if j := a.newJev(a.cfg.AI.JevKeyEnv, a.cfg.AI.JevEndpoint); j != nil {
		t.Judge = triage.JevJudge{Ranker: j}
	}
	if c := a.newAgent(a.cfg.AI.Agent, a.cfg.AI.Model); c != nil {
		if err := a.makeAIDir(); err != nil {
			return nil, err
		}
		t.Drafter = triage.AgentDrafter{Answerer: c}
	}
	if p, err := exec.LookPath("alerter"); err == nil {
		t.Notifier = triage.Alerter{Path: p}
	} else {
		a.logger().Warn("triage: no alerter on PATH, banners are off", "install", "brew install vjeantet/tap/alerter")
	}
	t.SetRules(triage.NewRules(a.cfg.Notifications))
	a.logger().Info("triage", "judge", t.Judge != nil, "drafter", t.Drafter != nil, "banners", t.Notifier != nil)
	return t, nil
}
