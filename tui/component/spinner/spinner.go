// Package spinner draws a loading/streaming indicator whose glyph turns and
// whose colour breathes: brightness eases between two stops on a slow cosine,
// the way a status LED pulses, while the glyph advances at its own faster rate.
//
// Frames are a pure function of time since Start, so one tick chain serves
// every place that shows the spinner, and a dropped or late tick never skews
// the cycle. The breathing palette is rendered once per Model, not per frame.
package spinner

import (
	"image/color"
	"math"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Glyph sets. Dots turns; Pulse holds still and lets the colour do the work.
var (
	Dots  = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	Pulse = []string{"●"}
)

const (
	// frameEvery paces both the glyph and the redraw. 12.5fps is smooth for a
	// one-cell glyph without putting the terminal on a render loop.
	frameEvery = 80 * time.Millisecond
	// breathEvery is one full dim→bright→dim cycle: near a resting breath, so
	// the pulse reads as alive rather than alarmed.
	breathEvery = 1600 * time.Millisecond
	// shades is how many steps the breath is quantized into. Beyond ~16 the
	// difference between neighbours is below what a terminal cell shows.
	shades = 16
)

// TickMsg advances a running spinner. It carries the tag of the chain that
// sent it so a chain left over from before a Stop/Start dies quietly.
type TickMsg struct {
	at  time.Time
	tag uint64
}

// Model is a spinner; build one with New. The zero value draws nothing.
type Model struct {
	glyphs  []string
	palette []lipgloss.Style
	start   time.Time
	now     time.Time
	tag     uint64
	on      bool
}

// New builds a spinner that breathes from dim to bright.
func New(glyphs []string, dim, bright color.Color) Model {
	stops := lipgloss.Blend1D(shades, dim, bright)
	palette := make([]lipgloss.Style, len(stops))
	for i, c := range stops {
		palette[i] = lipgloss.NewStyle().Foreground(c)
	}
	return Model{glyphs: glyphs, palette: palette}
}

// Running reports whether the spinner is animating.
func (m Model) Running() bool { return m.on }

// Start sets the spinner going from its first frame and returns the tick that
// drives it. Starting one already running is a no-op, so callers can ask on
// every transition into a busy state without stacking tick chains.
func (m *Model) Start(now time.Time) tea.Cmd {
	if m.on {
		return nil
	}
	m.on, m.start, m.now = true, now, now
	m.tag++
	return m.tick()
}

// Stop halts the spinner; the tick already in flight is dropped on arrival.
func (m *Model) Stop() { m.on = false }

// Update advances the spinner on its own tick and reports whether it did, so
// the caller knows a redraw is due. Any other message is ignored.
func (m *Model) Update(msg tea.Msg) (tea.Cmd, bool) {
	t, ok := msg.(TickMsg)
	if !ok || !m.on || t.tag != m.tag {
		return nil, false
	}
	m.now = t.at
	return m.tick(), true
}

func (m *Model) tick() tea.Cmd {
	tag := m.tag
	return tea.Tick(frameEvery, func(at time.Time) tea.Msg { return TickMsg{at: at, tag: tag} })
}

// View is the current frame: one glyph in the current shade. A stopped
// spinner draws its first glyph at full brightness, so a caller that forgot
// to Start still shows something honest rather than a blank.
func (m Model) View() string {
	if len(m.glyphs) == 0 || len(m.palette) == 0 {
		return ""
	}
	if !m.on {
		return m.palette[len(m.palette)-1].Render(m.glyphs[0])
	}
	el := m.now.Sub(m.start)
	g := m.glyphs[int(el/frameEvery)%len(m.glyphs)]
	return m.palette[shade(el, len(m.palette))].Render(g)
}

// shade maps elapsed time onto the palette: (1-cos)/2 starts dim, peaks
// mid-cycle, and eases at both ends the way a breath does.
func shade(el time.Duration, n int) int {
	phase := float64(el%breathEvery) / float64(breathEvery)
	b := (1 - math.Cos(2*math.Pi*phase)) / 2
	return int(math.Round(b * float64(n-1)))
}
