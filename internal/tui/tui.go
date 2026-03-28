// Package tui provides a read-only terminal dashboard for hub-router.
// Launch with: /hub-router -tui
// It polls GET /debug/stats on the running server and renders a live view.
package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jaar23/hub-router/internal/model"
	"github.com/jaar23/hub-router/internal/queue"
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

	styleKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("111")).
			Bold(true)
)

// sparkline characters ordered from empty to full
var sparkChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

const (
	historyLen      = 30
	refreshInterval = time.Second
	panelWidth      = 38
)

// ── messages ──────────────────────────────────────────────────────────────────

type statsMsg struct {
	stats *model.StatsResponse
	err   error
}

type tickMsg time.Time

// ── model ─────────────────────────────────────────────────────────────────────

type tuiModel struct {
	addr   string
	apiKey string
	client *http.Client

	stats      *model.StatsResponse
	lastUpdate time.Time
	fetchErr   error

	// ring buffer of total queue depths for sparkline
	depthHistory []int
	// throughput: (total enqueued delta) / interval
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

		// Compute aggregate totals across all keys
		totalDepth, totalEnqueued := aggregateTotals(msg.stats.Queues)

		// Update depth history ring buffer
		if len(m.depthHistory) >= historyLen {
			m.depthHistory = m.depthHistory[1:]
		}
		m.depthHistory = append(m.depthHistory, totalDepth)

		// Calculate throughput
		if prev != nil {
			delta := totalEnqueued - m.prevEnqueued
			if delta >= 0 {
				m.throughput = float64(delta) / refreshInterval.Seconds()
			}
		}
		m.prevEnqueued = totalEnqueued
	}
	return m, nil
}

// aggregateTotals sums depth and enqueued across all keys.
func aggregateTotals(queues map[string]model.QueueStats) (totalDepth int, totalEnqueued int64) {
	for _, qs := range queues {
		totalDepth += qs.Depth
		totalEnqueued += qs.EnqueuedTotal
	}
	return
}

// sortedKeys returns queue keys sorted with "default" first, then alphabetically.
func sortedKeys(queues map[string]model.QueueStats) []string {
	keys := make([]string, 0, len(queues))
	for k := range queues {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == queue.DefaultKey {
			return true
		}
		if keys[j] == queue.DefaultKey {
			return false
		}
		return keys[i] < keys[j]
	})
	return keys
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

	// ── queues panel (one row per key) ───────────────────────────────────────
	keys := sortedKeys(s.Queues)
	var queueRows strings.Builder
	for _, k := range keys {
		qs := s.Queues[k]
		label := styleKey.Render("[" + k + "]")
		depthStr := depthColor(qs.Depth, qs.Capacity)
		enqStr := styleLabel.Render(" enq:") + styleValue.Render(fmtInt(qs.EnqueuedTotal))
		deqStr := styleLabel.Render(" deq:") + styleValue.Render(fmtInt(qs.DequeuedTotal))
		queueRows.WriteString(label + " " + depthStr + "  " + enqStr + " " + deqStr + "\n")
		if qs.ExpiredTotal > 0 || qs.DroppedTotal > 0 {
			queueRows.WriteString(
				styleLabel.Render("       exp:") + warnColor(qs.ExpiredTotal) +
					styleLabel.Render(" drop:") + warnColor(qs.DroppedTotal) + "\n",
			)
		}
	}
	if len(keys) == 0 {
		queueRows.WriteString(styleDim.Render("(no queues yet)") + "\n")
	}
	queueContent := styleTitle.Render("QUEUES") + "\n" + queueRows.String()
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
	if colorFn != "" {
		return styleLabel.Render(label) + colorFn + "\n"
	}
	return styleLabel.Render(label) + styleValue.Render(value) + "\n"
}

func fmtInt(n int64) string {
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
	mi := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, mi, sec)
	}
	if mi > 0 {
		return fmt.Sprintf("%dm%02ds", mi, sec)
	}
	return fmt.Sprintf("%ds", sec)
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
