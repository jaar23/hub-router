// Package tui provides a read-only terminal dashboard for hub-router.
// Launch with: /hub-router -tui
// It polls GET /debug/stats on the running server and renders a live view.
package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jaar23/hub-router/internal/model"
)

// ── styles ────────────────────────────────────────────────────────────────────

var (
	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("62"))

	styleLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	styleValue = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Bold(true)

	styleWarn = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)

	styleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	styleGood = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")).
			Bold(true)

	styleDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("62")).
			Padding(0, 2)
)

// sparkline characters ordered from empty to full
var sparkChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

const (
	historyLen      = 30
	refreshInterval = time.Second
	panelWidth      = 32
)

// ── messages ──────────────────────────────────────────────────────────────────

type statsMsg struct {
	stats *model.StatsResponse
	err   error
}

type tickMsg time.Time

// ── model ─────────────────────────────────────────────────────────────────────

type tuiModel struct {
	addr    string
	apiKey  string
	client  *http.Client

	stats      *model.StatsResponse
	lastUpdate time.Time
	fetchErr   error

	// ring buffer of queue depths for sparkline
	depthHistory []int
	// throughput: (enqueued delta) / interval
	prevEnqueued int64
	throughput   float64
}

func newModel(addr, apiKey string) tuiModel {
	return tuiModel{
		addr:         addr,
		apiKey:       apiKey,
		client:       &http.Client{Timeout: 5 * time.Second},
		depthHistory: make([]int, 0, historyLen),
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(m.fetchCmd(), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m tuiModel) fetchCmd() tea.Cmd {
	return func() tea.Msg {
		url := m.addr + "/debug/stats"
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return statsMsg{err: err}
		}
		if m.apiKey != "" {
			req.Header.Set("X-Admin-API-Key", m.apiKey)
		}
		resp, err := m.client.Do(req)
		if err != nil {
			return statsMsg{err: fmt.Errorf("connect: %w", err)}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return statsMsg{err: fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
		}
		var s model.StatsResponse
		if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
			return statsMsg{err: fmt.Errorf("decode: %w", err)}
		}
		return statsMsg{stats: &s}
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			return m, m.fetchCmd()
		}

	case tickMsg:
		return m, tea.Batch(m.fetchCmd(), tickCmd())

	case statsMsg:
		if msg.err != nil {
			m.fetchErr = msg.err
			return m, nil
		}
		m.fetchErr = nil
		prev := m.stats
		m.stats = msg.stats
		m.lastUpdate = time.Now()

		// Update depth history ring buffer
		depth := msg.stats.Queue.Depth
		if len(m.depthHistory) >= historyLen {
			m.depthHistory = m.depthHistory[1:]
		}
		m.depthHistory = append(m.depthHistory, depth)

		// Calculate throughput
		if prev != nil {
			delta := msg.stats.Queue.EnqueuedTotal - m.prevEnqueued
			if delta >= 0 {
				m.throughput = float64(delta) / refreshInterval.Seconds()
			}
		}
		m.prevEnqueued = msg.stats.Queue.EnqueuedTotal
	}
	return m, nil
}

// ── view ──────────────────────────────────────────────────────────────────────

func (m tuiModel) View() string {
	if m.stats == nil && m.fetchErr != nil {
		return renderConnecting(m.addr, m.fetchErr)
	}
	if m.stats == nil {
		return renderConnecting(m.addr, nil)
	}
	return renderDashboard(m)
}

func renderConnecting(addr string, err error) string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(styleHeader.Render(" hub-router "))
	sb.WriteString("\n\n")
	if err != nil {
		sb.WriteString(styleError.Render("  ✗ " + err.Error()))
		sb.WriteString("\n")
		sb.WriteString(styleDim.Render("  connecting to " + addr + " …"))
	} else {
		sb.WriteString(styleDim.Render("  connecting to " + addr + " …"))
	}
	sb.WriteString("\n\n")
	sb.WriteString(styleDim.Render("  [q] quit"))
	return sb.String()
}

func renderDashboard(m tuiModel) string {
	s := m.stats

	// ── header ──────────────────────────────────────────────────────────────
	uptime := formatDuration(time.Duration(s.UptimeSeconds) * time.Second)
	header := styleHeader.Render(" hub-router ") +
		"  " + styleDim.Render("uptime: "+uptime)

	// ── queue panel ──────────────────────────────────────────────────────────
	queueContent := styleTitle.Render("QUEUE") + "\n" +
		row("depth    ", fmt.Sprintf("%d / %s", s.Queue.Depth, fmtInt(int64(s.Queue.Capacity))), depthColor(s.Queue.Depth, s.Queue.Capacity)) +
		row("enqueued ", fmtInt(s.Queue.EnqueuedTotal), "") +
		row("dequeued ", fmtInt(s.Queue.DequeuedTotal), "") +
		row("expired  ", fmtInt(s.Queue.ExpiredTotal), warnColor(s.Queue.ExpiredTotal)) +
		row("dropped  ", fmtInt(s.Queue.DroppedTotal), warnColor(s.Queue.DroppedTotal))
	queuePanel := styleBorder.Width(panelWidth).Render(queueContent)

	// ── throughput panel ─────────────────────────────────────────────────────
	sparkline := renderSparkline(m.depthHistory)
	tpContent := styleTitle.Render("THROUGHPUT") + "\n" +
		sparkline + "\n" +
		styleLabel.Render("enqueue rate  ") + styleValue.Render(fmt.Sprintf("%.1f req/s", m.throughput))
	tpPanel := styleBorder.Width(panelWidth).Render(tpContent)

	// ── store panel ──────────────────────────────────────────────────────────
	storeContent := styleTitle.Render("STORE") + "\n" +
		row("results  ", fmt.Sprintf("%d", s.Store.Results), "") +
		row("waiters  ", fmt.Sprintf("%d", s.Store.ActiveWaiters), activeColor(s.Store.ActiveWaiters))
	storePanel := styleBorder.Width(panelWidth).Render(storeContent)

	// ── security panel ───────────────────────────────────────────────────────
	rlStatus := styleGood.Render("on")
	if !s.Security.RateLimitEnabled {
		rlStatus = styleDim.Render("off")
	}
	secContent := styleTitle.Render("SECURITY") + "\n" +
		styleLabel.Render("rate limiting  ") + rlStatus + "\n" +
		row("tracked IPs  ", fmt.Sprintf("%d", s.Security.TrackedIPs), "") +
		row("throttled    ", fmt.Sprintf("%d", s.Security.ThrottledIPs), warnColor(int64(s.Security.ThrottledIPs))) +
		row("locked IPs   ", fmt.Sprintf("%d", s.Security.LockedIPs), alertColor(int64(s.Security.LockedIPs))) +
		row("watched IPs  ", fmt.Sprintf("%d", s.Security.WatchedIPs), warnColor(int64(s.Security.WatchedIPs)))
	secPanel := styleBorder.Width(panelWidth).Render(secContent)

	// ── footer ───────────────────────────────────────────────────────────────
	updated := styleDim.Render("updated: " + m.lastUpdate.Format("15:04:05.000"))
	var errStr string
	if m.fetchErr != nil {
		errStr = styleError.Render("  ⚠ " + m.fetchErr.Error())
	}
	footer := styleDim.Render("[q] quit  [r] refresh") + "    " + updated + errStr

	// ── layout ───────────────────────────────────────────────────────────────
	leftCol := lipgloss.JoinVertical(lipgloss.Left, queuePanel, tpPanel)
	rightCol := lipgloss.JoinVertical(lipgloss.Left, storePanel, secPanel)
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, "  ", rightCol)

	return "\n" + header + "\n\n" +
		lipgloss.NewStyle().PaddingLeft(2).Render(body) +
		"\n\n" +
		lipgloss.NewStyle().PaddingLeft(2).Render(footer) +
		"\n"
}

// ── helpers ───────────────────────────────────────────────────────────────────

func row(label, value, colorFn string) string {
	v := styleValue.Render(value)
	if colorFn != "" {
		v = colorFn
	}
	_ = v
	// Re-apply based on flag
	if colorFn != "" {
		return styleLabel.Render(label) + colorFn + "\n"
	}
	return styleLabel.Render(label) + styleValue.Render(value) + "\n"
}

func fmtInt(n int64) string {
	// Add thousands separators
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	start := len(s) % 3
	if start > 0 {
		b.WriteString(s[:start])
	}
	for i := start; i < len(s); i += 3 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func depthColor(depth, capacity int) string {
	if capacity == 0 {
		return styleValue.Render(fmt.Sprintf("%d / 0", depth))
	}
	pct := float64(depth) / float64(capacity)
	val := fmt.Sprintf("%d / %s", depth, fmtInt(int64(capacity)))
	switch {
	case pct > 0.8:
		return styleError.Render(val)
	case pct > 0.5:
		return styleWarn.Render(val)
	default:
		return styleValue.Render(val)
	}
}

func warnColor(n int64) string {
	if n > 0 {
		return styleWarn.Render(fmtInt(n))
	}
	return styleValue.Render("0")
}

func alertColor(n int64) string {
	if n > 0 {
		return styleError.Render(fmtInt(n))
	}
	return styleGood.Render("0")
}

func activeColor(n int) string {
	if n > 0 {
		return styleGood.Render(fmt.Sprintf("%d", n))
	}
	return styleValue.Render("0")
}

func renderSparkline(history []int) string {
	if len(history) == 0 {
		return styleDim.Render("no data yet")
	}
	maxVal := 1
	for _, v := range history {
		if v > maxVal {
			maxVal = v
		}
	}
	var b strings.Builder
	for _, v := range history {
		idx := int(float64(v) / float64(maxVal) * float64(len(sparkChars)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkChars) {
			idx = len(sparkChars) - 1
		}
		b.WriteRune(sparkChars[idx])
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("62")).Render(b.String())
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// ── public entry point ────────────────────────────────────────────────────────

// Run starts the TUI and blocks until the user quits (q / ctrl+c).
// addr is the hub-router base URL (e.g. "http://127.0.0.1:8080").
// apiKey is the X-Admin-API-Key value (empty string if not configured).
func Run(addr, apiKey string) error {
	p := tea.NewProgram(
		newModel(addr, apiKey),
		tea.WithAltScreen(),
	)
	_, err := p.Run()
	return err
}
