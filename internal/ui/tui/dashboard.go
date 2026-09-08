package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/andocodes/cassie/internal/domain/catalog"
	runtimeDomain "github.com/andocodes/cassie/internal/domain/runtime"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	processLimit = 200
	logTailBytes = 64 << 10
	maxLogBytes  = 256 << 10
)

type Entry struct {
	Application  catalog.Application
	Address      string
	Linked       bool
	Trusted      bool
	TrustSummary string
}

type PlatformState string

const (
	PlatformChecking PlatformState = "checking"
	PlatformReady    PlatformState = "ready"
	PlatformLogin    PlatformState = "login required"
	PlatformFailed   PlatformState = "unavailable"
)

type Platform struct {
	State  PlatformState
	Detail string
}

// Backend keeps the Bubble Tea model independent of daemon, platform, and OS
// adapters. Long-running watches must stop when ctx is cancelled.
type Backend interface {
	Refresh(context.Context) ([]Entry, error)
	EnsurePlatform(context.Context) Platform
	WatchProcesses(context.Context, int) ([]runtimeDomain.Process, <-chan runtimeDomain.ProcessEvent, <-chan error, error)
	WatchLogs(context.Context, string, int64) ([]byte, <-chan []byte, <-chan error, error)
	Start(context.Context, Entry) (runtimeDomain.Process, error)
	Stop(context.Context, string) error
	Trust(context.Context, Entry) error
	Open(context.Context, string) error
}

type Options struct {
	Root    string
	Entries []Entry
	Backend Backend
}

type item struct {
	entry     Entry
	state     string
	processID string
}

func (i item) Title() string       { return i.entry.Application.Name }
func (i item) Description() string { return i.state }
func (i item) FilterValue() string {
	app := i.entry.Application
	return strings.Join([]string{app.Name, app.Root, app.Domain, i.state}, " ")
}

type rowDelegate struct{}

func (rowDelegate) Height() int                         { return 1 }
func (rowDelegate) Spacing() int                        { return 0 }
func (rowDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (rowDelegate) Render(writer io.Writer, model list.Model, index int, value list.Item) {
	row, ok := value.(item)
	if !ok {
		return
	}
	indicator, indicatorStyle := stateIndicator(row.state)
	name := truncate(row.entry.Application.Name, max(model.Width()-lipgloss.Width(row.state)-5, 4))
	gap := max(model.Width()-lipgloss.Width(indicator)-lipgloss.Width(name)-lipgloss.Width(row.state)-3, 1)
	line := indicatorStyle.Render(indicator) + " " + name + strings.Repeat(" ", gap) + mutedStyle.Render(row.state)
	if index == model.Index() {
		line = selectedStyle.Width(max(model.Width(), lipgloss.Width(line))).Render(line)
	}
	_, _ = fmt.Fprint(writer, line)
}

type overlay int

const (
	overlayNone overlay = iota
	overlayDetected
	overlayPlatform
	overlayHelp
	overlayTrust
)

type dashboard struct {
	ctx        context.Context
	backend    Backend
	root       string
	entries    []Entry
	list       list.Model
	detected   list.Model
	processes  map[string]runtimeDomain.Process
	busy       map[string]string
	platform   Platform
	logs       viewport.Model
	logText    string
	logID      string
	logCancel  context.CancelFunc
	logFollow  bool
	overlay    overlay
	pending    *Entry
	notice     string
	width      int
	height     int
	showDetail bool
	err        error
}

type processStreamMsg struct {
	processes []runtimeDomain.Process
	events    <-chan runtimeDomain.ProcessEvent
	errors    <-chan error
}

type processEventMsg struct {
	event  runtimeDomain.ProcessEvent
	events <-chan runtimeDomain.ProcessEvent
	errors <-chan error
}

type processStreamErrorMsg struct{ err error }

type logStreamMsg struct {
	id      string
	initial []byte
	chunks  <-chan []byte
	errors  <-chan error
}

type logChunkMsg struct {
	id     string
	chunk  []byte
	chunks <-chan []byte
	errors <-chan error
}

type logClosedMsg struct{ id string }

type logErrorMsg struct {
	id  string
	err error
}

type platformMsg struct{ platform Platform }

type refreshMsg struct {
	entries []Entry
	err     error
}

type actionMsg struct {
	action  string
	entry   Entry
	process runtimeDomain.Process
	err     error
}

func Run(ctx context.Context, options Options) error {
	if options.Backend == nil {
		return fmt.Errorf("TUI backend is required")
	}
	model := newDashboard(ctx, options)
	result, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	finished, ok := result.(*dashboard)
	if ok && finished.err != nil {
		return finished.err
	}
	return nil
}

func newDashboard(ctx context.Context, options Options) *dashboard {
	d := &dashboard{
		ctx:       ctx,
		backend:   options.Backend,
		root:      options.Root,
		entries:   options.Entries,
		processes: make(map[string]runtimeDomain.Process),
		busy:      make(map[string]string),
		platform:  Platform{State: PlatformChecking, Detail: "Checking Docker and Cassie services"},
		width:     100,
		height:    24,
	}
	d.list = newList(nil)
	d.detected = newList(nil)
	d.logs = viewport.New(40, 10)
	d.rebuildLists()
	d.resize()
	return d
}

func newList(items []list.Item) list.Model {
	model := list.New(items, rowDelegate{}, 32, 12)
	model.SetShowTitle(false)
	model.SetShowStatusBar(false)
	model.SetShowHelp(false)
	model.SetShowPagination(false)
	model.SetFilteringEnabled(true)
	model.DisableQuitKeybindings()
	model.FilterInput.Prompt = "/ "
	return model
}

func (m *dashboard) Init() tea.Cmd {
	return tea.Batch(m.watchProcesses(), m.ensurePlatform(), m.refresh())
}

func (m *dashboard) watchProcesses() tea.Cmd {
	return func() tea.Msg {
		processes, events, errorsChannel, err := m.backend.WatchProcesses(m.ctx, processLimit)
		if err != nil {
			return processStreamErrorMsg{err: err}
		}
		return processStreamMsg{processes: processes, events: events, errors: errorsChannel}
	}
}

func (m *dashboard) ensurePlatform() tea.Cmd {
	return func() tea.Msg { return platformMsg{platform: m.backend.EnsurePlatform(m.ctx)} }
}

func (m *dashboard) refresh() tea.Cmd {
	return func() tea.Msg {
		entries, err := m.backend.Refresh(m.ctx)
		return refreshMsg{entries: entries, err: err}
	}
}

func (m *dashboard) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch message := message.(type) {
	case tea.KeyMsg:
		if command := m.handleKey(message); command != nil {
			commands = append(commands, command)
		}
	case tea.WindowSizeMsg:
		m.width = max(message.Width, 36)
		m.height = max(message.Height, 10)
		m.resize()
	case processStreamMsg:
		for _, process := range message.processes {
			m.processes[process.ID] = process
		}
		m.rebuildLists()
		commands = append(commands, waitForProcessEvent(message.events, message.errors), m.watchSelectedLog())
	case processEventMsg:
		m.processes[message.event.Process.ID] = message.event.Process
		delete(m.busy, applicationKey(message.event.Process.App, message.event.Process.Root))
		m.rebuildLists()
		commands = append(commands, waitForProcessEvent(message.events, message.errors), m.watchSelectedLog())
	case processStreamErrorMsg:
		m.notice = "Daemon: " + message.err.Error()
	case logStreamMsg:
		if message.id == m.logID {
			m.logText = string(message.initial)
			m.renderLogs(true)
			commands = append(commands, waitForLog(message.id, message.chunks, message.errors))
		}
	case logChunkMsg:
		if message.id == m.logID {
			m.appendLog(message.chunk)
			commands = append(commands, waitForLog(message.id, message.chunks, message.errors))
		}
	case logClosedMsg:
		if message.id == m.logID && m.logText == "" {
			m.logText = "Process ended without output."
			m.renderLogs(true)
		}
	case logErrorMsg:
		if message.id == m.logID {
			m.notice = "Logs: " + message.err.Error()
		}
	case platformMsg:
		m.platform = message.platform
	case refreshMsg:
		if message.err != nil {
			m.notice = "Discovery: " + message.err.Error()
		} else {
			m.entries = message.entries
			m.rebuildLists()
		}
	case actionMsg:
		delete(m.busy, entryKey(message.entry))
		if message.err != nil {
			m.notice = titleWord(message.action) + ": " + message.err.Error()
		} else {
			m.notice = ""
			if message.process.ID != "" {
				m.processes[message.process.ID] = message.process
			}
			if message.action == "trust" {
				m.markTrusted(message.entry)
			}
		}
		m.rebuildLists()
	}

	if m.overlay == overlayDetected {
		var command tea.Cmd
		m.detected, command = m.detected.Update(message)
		commands = append(commands, command)
	} else if m.overlay == overlayNone {
		before := m.selectedKey()
		var command tea.Cmd
		m.list, command = m.list.Update(message)
		commands = append(commands, command)
		if before != m.selectedKey() {
			commands = append(commands, m.watchSelectedLog())
		}
	}
	m.resize()
	return m, tea.Batch(commands...)
}

func (m *dashboard) handleKey(message tea.KeyMsg) tea.Cmd {
	key := message.String()
	if m.list.SettingFilter() && m.overlay == overlayNone {
		return nil
	}
	if m.overlay == overlayDetected && m.detected.SettingFilter() {
		return nil
	}
	if key == "ctrl+c" || key == "q" && m.overlay == overlayNone {
		if m.logCancel != nil {
			m.logCancel()
		}
		return tea.Quit
	}
	if m.overlay != overlayNone {
		switch key {
		case "esc", "q", "p", "d", "?":
			m.overlay = overlayNone
			m.pending = nil
		case "u":
			if m.overlay == overlayPlatform {
				m.platform = Platform{State: PlatformChecking, Detail: "Checking Docker and Cassie services"}
				return m.ensurePlatform()
			}
		case "t":
			if m.overlay == overlayTrust && m.pending != nil {
				entry := *m.pending
				m.overlay = overlayNone
				m.pending = nil
				m.busy[entryKey(entry)] = "trusting"
				return m.trustAndStart(entry)
			}
		}
		return nil
	}

	switch key {
	case "p":
		m.overlay = overlayPlatform
	case "d":
		m.overlay = overlayDetected
	case "?":
		m.overlay = overlayHelp
	case "tab":
		if m.width < 72 {
			m.showDetail = !m.showDetail
		}
	case "enter", "r":
		entry, ok := m.selectedEntry()
		if !ok {
			return nil
		}
		if len(entry.Application.Commands) == 0 {
			m.notice = "No command configured for " + entry.Application.Name
			return nil
		}
		if process, ok := m.runningProcess(entry); ok {
			m.notice = process.App + " is already running"
			return nil
		}
		if entry.TrustSummary != "" && !entry.Trusted {
			m.pending = &entry
			m.overlay = overlayTrust
			return nil
		}
		m.busy[entryKey(entry)] = "starting"
		return m.start(entry)
	case "s":
		entry, ok := m.selectedEntry()
		if !ok {
			return nil
		}
		process, ok := m.runningProcess(entry)
		if !ok {
			m.notice = entry.Application.Name + " is not running"
			return nil
		}
		m.busy[entryKey(entry)] = "stopping"
		return m.stop(entry, process.ID)
	case "R":
		entry, ok := m.selectedEntry()
		if !ok || len(entry.Application.Commands) == 0 {
			return nil
		}
		if entry.TrustSummary != "" && !entry.Trusted {
			m.pending = &entry
			m.overlay = overlayTrust
			return nil
		}
		process, running := m.runningProcess(entry)
		m.busy[entryKey(entry)] = "restarting"
		return m.restart(entry, process.ID, running)
	case "o":
		entry, ok := m.selectedEntry()
		if ok {
			return m.open(entry)
		}
	case "pgup":
		m.logFollow = false
		m.logs.HalfViewUp()
	case "pgdown":
		m.logs.HalfViewDown()
		m.logFollow = m.logs.AtBottom()
	case "end":
		m.logFollow = true
		m.logs.GotoBottom()
	}
	return nil
}

func (m *dashboard) start(entry Entry) tea.Cmd {
	return func() tea.Msg {
		process, err := m.backend.Start(m.ctx, entry)
		return actionMsg{action: "start", entry: entry, process: process, err: err}
	}
}

func (m *dashboard) stop(entry Entry, id string) tea.Cmd {
	return func() tea.Msg {
		err := m.backend.Stop(m.ctx, id)
		return actionMsg{action: "stop", entry: entry, err: err}
	}
}

func (m *dashboard) restart(entry Entry, id string, running bool) tea.Cmd {
	return func() tea.Msg {
		if running {
			if err := m.backend.Stop(m.ctx, id); err != nil {
				return actionMsg{action: "restart", entry: entry, err: err}
			}
		}
		process, err := m.backend.Start(m.ctx, entry)
		return actionMsg{action: "restart", entry: entry, process: process, err: err}
	}
}

func (m *dashboard) trustAndStart(entry Entry) tea.Cmd {
	return func() tea.Msg {
		if err := m.backend.Trust(m.ctx, entry); err != nil {
			return actionMsg{action: "trust", entry: entry, err: err}
		}
		entry.Trusted = true
		process, err := m.backend.Start(m.ctx, entry)
		return actionMsg{action: "trust", entry: entry, process: process, err: err}
	}
}

func (m *dashboard) open(entry Entry) tea.Cmd {
	return func() tea.Msg {
		err := m.backend.Open(m.ctx, entryURL(entry))
		return actionMsg{action: "open", entry: entry, err: err}
	}
}

func (m *dashboard) watchSelectedLog() tea.Cmd {
	entry, ok := m.selectedEntry()
	process, hasProcess := m.processFor(entry)
	if ok && hasProcess && process.ID == m.logID && m.logCancel != nil {
		return nil
	}
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	if !ok || !hasProcess || process.ID == "" {
		if m.logID != "" {
			m.logText = ""
			m.renderLogs(false)
		}
		m.logID = ""
		return nil
	}
	m.logID = process.ID
	m.logText = "Loading logs…"
	m.logFollow = true
	m.renderLogs(true)
	ctx, cancel := context.WithCancel(m.ctx)
	m.logCancel = cancel
	return func() tea.Msg {
		initial, chunks, errorsChannel, err := m.backend.WatchLogs(ctx, process.ID, logTailBytes)
		if err != nil {
			return logErrorMsg{id: process.ID, err: err}
		}
		return logStreamMsg{id: process.ID, initial: initial, chunks: chunks, errors: errorsChannel}
	}
}

func waitForProcessEvent(events <-chan runtimeDomain.ProcessEvent, errorsChannel <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case event, open := <-events:
			if !open {
				return processStreamErrorMsg{err: fmt.Errorf("daemon event stream closed")}
			}
			return processEventMsg{event: event, events: events, errors: errorsChannel}
		case err, open := <-errorsChannel:
			if !open {
				return processStreamErrorMsg{err: fmt.Errorf("daemon event stream closed")}
			}
			return processStreamErrorMsg{err: err}
		}
	}
}

func waitForLog(id string, chunks <-chan []byte, errorsChannel <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case chunk, open := <-chunks:
			if !open {
				return logClosedMsg{id: id}
			}
			return logChunkMsg{id: id, chunk: chunk, chunks: chunks, errors: errorsChannel}
		case err, open := <-errorsChannel:
			if !open {
				return logClosedMsg{id: id}
			}
			return logErrorMsg{id: id, err: err}
		}
	}
}

func (m *dashboard) appendLog(chunk []byte) {
	m.logText += string(chunk)
	if len(m.logText) > maxLogBytes {
		m.logText = m.logText[len(m.logText)-maxLogBytes:]
	}
	m.renderLogs(true)
}

func (m *dashboard) renderLogs(follow bool) {
	m.logs.SetContent(strings.TrimRight(m.logText, "\n"))
	if follow && m.logFollow {
		m.logs.GotoBottom()
	}
}

func (m *dashboard) rebuildLists() {
	selected := m.selectedKey()
	linkedItems, detectedItems := m.items()
	m.list.SetItems(linkedItems)
	m.detected.SetItems(detectedItems)
	if selected != "" {
		for index, value := range linkedItems {
			if row, ok := value.(item); ok && entryKey(row.entry) == selected {
				m.list.Select(index)
				break
			}
		}
	}
}

func (m *dashboard) items() ([]list.Item, []list.Item) {
	linked := make([]list.Item, 0, len(m.entries))
	detected := make([]list.Item, 0, len(m.entries))
	known := make(map[string]struct{}, len(m.entries))
	for _, entry := range m.entries {
		known[entryKey(entry)] = struct{}{}
		row := item{entry: entry, state: m.entryState(entry)}
		if process, ok := m.processFor(entry); ok {
			row.processID = process.ID
		}
		if entry.Linked {
			linked = append(linked, row)
		} else {
			row.state = "detected"
			detected = append(detected, row)
		}
	}
	for _, process := range m.processes {
		key := applicationKey(process.App, process.Root)
		if process.Status != runtimeDomain.StatusRunning {
			continue
		}
		if _, exists := known[key]; exists {
			continue
		}
		entry := Entry{Application: catalog.Application{
			Name: process.App, Domain: catalog.SuggestedDomain(process.App), Root: process.Root,
		}, Linked: true, Trusted: true}
		linked = append(linked, item{entry: entry, state: string(process.Status), processID: process.ID})
	}
	return linked, detected
}

func dashboardItems(entries []Entry) ([]list.Item, int, int) {
	model := dashboard{entries: entries, processes: make(map[string]runtimeDomain.Process), busy: make(map[string]string)}
	linked, detected := model.items()
	return linked, len(linked), len(detected)
}

func (m *dashboard) entryState(entry Entry) string {
	if state := m.busy[entryKey(entry)]; state != "" {
		return state
	}
	if process, ok := m.processFor(entry); ok {
		return string(process.Status)
	}
	if len(entry.Application.Commands) == 0 {
		return "needs command"
	}
	return "ready"
}

func (m *dashboard) processFor(entry Entry) (runtimeDomain.Process, bool) {
	var selected runtimeDomain.Process
	found := false
	for _, process := range m.processes {
		if process.App != entry.Application.Name || filepath.Clean(process.Root) != filepath.Clean(entry.Application.Root) {
			continue
		}
		preferRunning := process.Status == runtimeDomain.StatusRunning && selected.Status != runtimeDomain.StatusRunning
		sameClass := (process.Status == runtimeDomain.StatusRunning) == (selected.Status == runtimeDomain.StatusRunning)
		if !found || preferRunning || sameClass && process.StartedAt.After(selected.StartedAt) {
			selected = process
			found = true
		}
	}
	return selected, found
}

func (m *dashboard) runningProcess(entry Entry) (runtimeDomain.Process, bool) {
	process, ok := m.processFor(entry)
	return process, ok && process.Status == runtimeDomain.StatusRunning
}

func (m *dashboard) selectedEntry() (Entry, bool) {
	selected, ok := m.list.SelectedItem().(item)
	return selected.entry, ok
}

func (m *dashboard) selectedKey() string {
	entry, ok := m.selectedEntry()
	if !ok {
		return ""
	}
	return entryKey(entry)
}

func (m *dashboard) markTrusted(entry Entry) {
	for index := range m.entries {
		if entryKey(m.entries[index]) == entryKey(entry) {
			m.entries[index].Trusted = true
		}
	}
}

func entryKey(entry Entry) string {
	return applicationKey(entry.Application.Name, entry.Application.Root)
}

func applicationKey(name, root string) string { return name + "\x00" + filepath.Clean(root) }

func (m *dashboard) resize() {
	bodyHeight := max(m.height-4, 6)
	if m.width < 72 {
		m.list.SetSize(max(m.width-4, 12), max(bodyHeight-2, 4))
		m.detected.SetSize(max(m.width-10, 12), max(bodyHeight-10, 4))
		m.logs.Width = max(m.width-6, 12)
		m.logs.Height = max(bodyHeight-8, 3)
		return
	}
	leftWidth := min(40, max(28, m.width/3))
	rightWidth := max(m.width-leftWidth-4, 28)
	m.list.SetSize(max(leftWidth-4, 12), max(bodyHeight-2, 4))
	m.detected.SetSize(max(m.width-14, 12), max(bodyHeight-10, 4))
	m.logs.Width = max(rightWidth-4, 12)
	m.logs.Height = max(bodyHeight-9, 3)
}

func (m *dashboard) View() string {
	header := m.header()
	bodyHeight := max(m.height-4, 6)
	var body string
	if m.width < 72 {
		content := m.list.View()
		if m.showDetail {
			content = m.detail(max(m.width-6, 20), bodyHeight-2)
		}
		body = panelStyle.Width(max(m.width-2, 20)).Height(bodyHeight).Render(content)
	} else {
		leftWidth := min(40, max(28, m.width/3))
		rightWidth := max(m.width-leftWidth-4, 28)
		left := panelStyle.Width(leftWidth).Height(bodyHeight).Render(m.list.View())
		right := panelStyle.Width(rightWidth).Height(bodyHeight).Render(m.detail(rightWidth-4, bodyHeight-2))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	if m.overlay != overlayNone {
		body = m.renderOverlay(bodyHeight)
	}
	return header + "\n" + body + "\n" + m.footer()
}

func (m *dashboard) header() string {
	brand := brandStyle.Render("🦁 CASSIE")
	location := shortenHome(m.root)
	linked, detected := len(m.list.Items()), 0
	for _, entry := range m.entries {
		if !entry.Linked {
			detected++
		}
	}
	running := 0
	for _, process := range m.processes {
		if process.Status == runtimeDomain.StatusRunning {
			running++
		}
	}
	counts := fmt.Sprintf("%d apps · %d running", linked, running)
	if detected > 0 {
		counts += fmt.Sprintf(" · %d detected", detected)
	}
	platform := platformLabel(m.platform)
	middle := strings.TrimSpace(strings.Join([]string{location, counts}, "  ·  "))
	middle = truncate(middle, max(m.width-lipgloss.Width(brand)-lipgloss.Width(platform)-4, 4))
	gap := max(m.width-lipgloss.Width(brand)-lipgloss.Width(middle)-lipgloss.Width(platform)-4, 1)
	return brand + "  " + mutedStyle.Render(middle) + strings.Repeat(" ", gap) + platform
}

func (m *dashboard) detail(width, height int) string {
	entry, ok := m.selectedEntry()
	if !ok {
		message := "No linked applications"
		if len(m.entries) > 0 {
			message += "\n\nPress d to review detected repositories."
		} else {
			message += "\n\nRun cassie link from an application directory."
		}
		return detailStyle.MaxWidth(width).Render(wrap(message, width))
	}
	app := entry.Application
	state := m.entryState(entry)
	status := stateStyle(state).Render(strings.ToUpper(state))
	lines := []string{
		titleStyle.Render(app.Name) + "  " + status,
		linkStyle.Render(entryURL(entry)),
		mutedStyle.Render(app.Root),
	}
	if m.notice != "" {
		lines = append(lines, "", warningStyle.Render(wrap(m.notice, width)))
	}
	lines = append(lines, "", sectionStyle.Render("LOGS"))
	logHeight := max(height-len(lines)-2, 3)
	m.logs.Width = max(width, 12)
	m.logs.Height = logHeight
	if m.logText == "" {
		message := "Logs appear here when the application starts."
		if process, exists := m.processFor(entry); exists && process.Status != runtimeDomain.StatusRunning {
			message = "No saved output for the latest session."
		}
		lines = append(lines, mutedStyle.Render(message))
	} else {
		lines = append(lines, m.logs.View())
	}
	return detailStyle.MaxWidth(width).MaxHeight(height).Render(strings.Join(lines, "\n"))
}

func entryURL(entry Entry) string {
	if entry.Address != "" {
		return entry.Address
	}
	return entry.Application.URL()
}

func (m *dashboard) renderOverlay(height int) string {
	width := min(max(m.width-12, 28), 88)
	content := ""
	title := ""
	switch m.overlay {
	case overlayDetected:
		title = "DETECTED REPOSITORIES"
		if len(m.detected.Items()) == 0 {
			content = mutedStyle.Render("No unlinked repositories found.")
		} else {
			content = m.detected.View() + "\n\n" + mutedStyle.Render("Link one from its directory with cassie link.")
		}
	case overlayPlatform:
		title = "PLATFORM"
		content = platformLabel(m.platform) + "\n\n" + wrap(m.platform.Detail, width-4)
		if m.platform.State == PlatformFailed {
			content += "\n\n" + mutedStyle.Render(wrap("Press u to retry. Cassie never starts Docker Desktop, OrbStack, or Colima.", width-4))
		}
		if m.platform.State == PlatformLogin {
			content += "\n\n" + mutedStyle.Render("Run cassie up to sign in to Infisical. Apps without secrets can still run.")
		}
	case overlayHelp:
		title = "KEYS"
		content = strings.Join([]string{
			"↑/↓ or j/k   select application",
			"/            filter applications",
			"r or enter   run",
			"s            stop",
			"R            restart",
			"o            open local URL",
			"p            platform",
			"d            detected repositories",
			"PgUp/PgDn    scroll logs; End follows",
			"q            leave apps running and quit",
		}, "\n")
	case overlayTrust:
		title = "TRUST REPOSITORY COMMANDS"
		if m.pending != nil {
			content = wrap(m.pending.TrustSummary, width-4) + "\n\n" + warningStyle.Render("Press t to trust this configuration and run it.")
		}
	}
	box := overlayStyle.Width(width).MaxHeight(max(height-2, 6)).Render(sectionStyle.Render(title) + "\n\n" + content)
	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, box)
}

func (m *dashboard) footer() string {
	if m.overlay != overlayNone {
		if m.overlay == overlayTrust {
			return footerStyle.Render("t trust + run   esc close")
		}
		return footerStyle.Render("esc close")
	}
	keys := "/ filter   r run   s stop   R restart   o open   p platform   d detected   ? help   q quit"
	if m.width < 72 {
		keys = "tab switch   / filter   r run   s stop   ? help   q quit"
	} else if m.width < 105 {
		keys = "/ filter   r run   s stop   R restart   o open   p platform   ? help   q quit"
	}
	return footerStyle.MaxWidth(m.width).Render(keys)
}

func platformLabel(platform Platform) string {
	switch platform.State {
	case PlatformReady:
		return passStyle.Render("● platform")
	case PlatformLogin:
		return warningStyle.Render("! login")
	case PlatformFailed:
		return failStyle.Render("× platform")
	default:
		return mutedStyle.Render("◌ platform")
	}
}

func stateIndicator(state string) (string, lipgloss.Style) {
	switch state {
	case "running":
		return "●", passStyle
	case "starting", "stopping", "restarting", "trusting":
		return "◌", warningStyle
	case "failed":
		return "×", failStyle
	case "needs command":
		return "!", warningStyle
	default:
		return "○", mutedStyle
	}
}

func stateStyle(state string) lipgloss.Style {
	_, style := stateIndicator(state)
	return style
}

func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func truncate(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func wrap(value string, width int) string {
	if width <= 1 {
		return value
	}
	paragraphs := strings.Split(value, "\n")
	lines := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if lipgloss.Width(paragraph) <= width {
			lines = append(lines, paragraph)
			continue
		}
		words := strings.Fields(paragraph)
		line := ""
		for _, word := range words {
			if lipgloss.Width(word) > width {
				word = truncate(word, width)
			}
			if line == "" {
				line = word
				continue
			}
			if lipgloss.Width(line)+1+lipgloss.Width(word) <= width {
				line += " " + word
				continue
			}
			lines = append(lines, line)
			line = word
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func titleWord(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

var (
	brandStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	sectionStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	passStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	failStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	linkStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Underline(true)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255")).Background(lipgloss.Color("237"))
	detailStyle   = lipgloss.NewStyle()
	panelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	overlayStyle  = lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).Padding(1, 2).Background(lipgloss.Color("235"))
	footerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)
