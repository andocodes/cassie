package tui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/andocodes/cassie/internal/domain/catalog"
	runtimeDomain "github.com/andocodes/cassie/internal/domain/runtime"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type linkingBackend struct {
	linked Entry
}

func (b *linkingBackend) Refresh(context.Context) ([]Entry, error) { return nil, nil }
func (b *linkingBackend) EnsurePlatform(context.Context) Platform {
	return Platform{State: PlatformReady}
}
func (b *linkingBackend) WatchProcesses(context.Context, int) ([]runtimeDomain.Process, <-chan runtimeDomain.ProcessEvent, <-chan error, error) {
	return nil, make(chan runtimeDomain.ProcessEvent), make(chan error), nil
}
func (b *linkingBackend) WatchLogs(context.Context, string, int64) ([]byte, <-chan []byte, <-chan error, error) {
	return nil, make(chan []byte), make(chan error), nil
}
func (b *linkingBackend) Link(_ context.Context, entry Entry) ([]Entry, error) {
	b.linked = entry
	entry.Linked = true
	entry.Application.Domain = catalog.SuggestedDomain(entry.Application.Name)
	entry.Application.Commands = []catalog.Command{{Run: "pnpm dev"}}
	return []Entry{entry}, nil
}
func (b *linkingBackend) Start(context.Context, Entry) (runtimeDomain.Process, error) {
	return runtimeDomain.Process{}, nil
}
func (b *linkingBackend) Stop(context.Context, string) error { return nil }
func (b *linkingBackend) Trust(context.Context, Entry) error { return nil }
func (b *linkingBackend) Open(context.Context, string) error { return nil }

func TestDashboardSeparatesLinkedApplicationsFromDetectedRepositories(t *testing.T) {
	items, linked, detected := dashboardItems([]Entry{
		{Application: catalog.Application{Name: "phoebe-ui", Domain: "phoebe-ui", Commands: []catalog.Command{{Run: "pnpm dev"}}}, Linked: true},
		{Application: catalog.Application{Name: "phoebe-api", Domain: "phoebe-api"}, Linked: true},
		{Application: catalog.Application{Name: "unlinked"}, Linked: false},
	})
	if linked != 2 || detected != 1 || len(items) != 2 {
		t.Fatalf("linked = %d, detected = %d, items = %d", linked, detected, len(items))
	}
	if got := items[1].(item).state; got != "needs command" {
		t.Fatalf("second application state = %q", got)
	}
}

func TestDashboardUsesDenseWorkspaceLayout(t *testing.T) {
	entry := Entry{
		Application: catalog.Application{Name: "phoebe-ui", Domain: "phoebe-ui", Root: "/work/phoebe-ui", Commands: []catalog.Command{{Run: "pnpm dev"}}},
		Linked:      true,
	}
	model := newDashboard(context.Background(), Options{Root: "/work", Entries: []Entry{entry}})
	model.platform = Platform{State: PlatformReady, Detail: "Ready"}
	view := model.View()
	for _, want := range []string{"🦁 CASSIE", "/work", "1 apps", "0 running", "platform", "phoebe-ui", "https://phoebe-ui.localhost", "LOGS"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard does not include %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"Link an app", "Set up this directory", "Recent development sessions", "Check local dependencies"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("dashboard includes obsolete menu item %q:\n%s", unwanted, view)
		}
	}
	if !strings.Contains(view, ansi.SetHyperlink("https://phoebe-ui.localhost")) {
		t.Fatalf("dashboard URL is not a terminal hyperlink:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > model.width {
			t.Fatalf("rendered line width = %d, want <= %d:\n%s", width, model.width, line)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines > model.height {
		t.Fatalf("rendered lines = %d, want <= %d", lines, model.height)
	}
}

func TestDashboardShowsRunningStateAndReconnectableLogs(t *testing.T) {
	entry := Entry{
		Application: catalog.Application{Name: "phoebe-api", Domain: "phoebe-api", Root: "/work/phoebe-api", Commands: []catalog.Command{{Run: "go run ."}}},
		Linked:      true,
	}
	model := newDashboard(context.Background(), Options{Root: "/work", Entries: []Entry{entry}})
	model.processes["process-1"] = runtimeDomain.Process{
		ID: "process-1", App: "phoebe-api", Root: "/work/phoebe-api", Status: runtimeDomain.StatusRunning,
	}
	model.rebuildLists()
	model.logText = "listening on :43127"
	model.renderLogs(true)
	view := model.View()
	for _, want := range []string{"1 running", "RUNNING", "listening on :43127"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard does not include %q:\n%s", want, view)
		}
	}
}

func TestDetectedRepositoriesOpenOnFirstPageAndSupportPaging(t *testing.T) {
	entries := make([]Entry, 0, 63)
	for index := range 63 {
		name := fmt.Sprintf("repository-with-a-long-name-%02d", index)
		entries = append(entries, Entry{Application: catalog.Application{Name: name, Domain: name, Root: "/work/" + name}})
	}
	model := newDashboard(context.Background(), Options{Root: "/work", Entries: entries})
	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	_, _ = model.Update(key("d"))

	if model.overlay != overlayDetected {
		t.Fatalf("overlay = %v, want detected", model.overlay)
	}
	if model.detected.Paginator.Page != 0 {
		t.Fatalf("opening detected repositories selected page %d, want 0", model.detected.Paginator.Page)
	}
	if !model.detected.ShowPagination() || model.detected.Paginator.TotalPages < 2 {
		t.Fatalf("pagination = visible %t, pages %d", model.detected.ShowPagination(), model.detected.Paginator.TotalPages)
	}
	var selected bytes.Buffer
	rowDelegate{}.Render(&selected, model.detected, 0, model.detected.Items()[0])
	selectedLine := ansi.Strip(selected.String())
	if strings.Contains(selectedLine, "\n") || !strings.Contains(selectedLine, "detected") {
		t.Fatalf("selected row must render on one line: %q", selectedLine)
	}
	if width := lipgloss.Width(selected.String()); width != model.detected.Width() {
		t.Fatalf("selected row width = %d, want %d", width, model.detected.Width())
	}

	_, _ = model.Update(key("j"))
	if model.detected.Index() != 1 {
		t.Fatalf("selection after j = %d, want 1", model.detected.Index())
	}

	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if model.detected.Paginator.Page != 1 {
		t.Fatalf("page after PgDown = %d, want 1", model.detected.Paginator.Page)
	}

	model.detected.GoToStart()
	_, _ = model.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if model.detected.Index() != 3 {
		t.Fatalf("selection after mouse wheel = %d, want 3", model.detected.Index())
	}

	model.detected.GoToEnd()
	if view := ansi.Strip(model.View()); !strings.Contains(view, "repository-with-a-long-name-62") {
		t.Fatalf("last detected repository is not visible:\n%s", view)
	}
}

func TestDetectedRepositoriesSearchButtonAndEnterLinkSelection(t *testing.T) {
	entry := Entry{Application: catalog.Application{Name: "phoebe-ui", Domain: "phoebe-ui", Root: "/work/phoebe-ui"}}
	backend := &linkingBackend{}
	model := newDashboard(context.Background(), Options{Root: "/work", Entries: []Entry{entry}, Backend: backend})
	_, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	_, _ = model.Update(key("d"))

	_, _ = model.Update(tea.MouseMsg{
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
		X:      1,
		Y:      model.height - 1,
	})
	if !model.detected.SettingFilter() {
		t.Fatal("clicking Search did not open the repository filter")
	}

	model.detected.ResetFilter()
	command := model.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil || model.overlay != overlayLink {
		t.Fatal("Enter did not open the repository name prompt")
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "LINK REPOSITORY") || !strings.Contains(view, "https://phoebe-ui.localhost") {
		t.Fatalf("link prompt is incomplete:\n%s", view)
	}

	model.linkName.SetValue("phoebe")
	command = model.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("Enter did not confirm the repository link")
	}
	message, ok := command().(linkMsg)
	if !ok {
		t.Fatalf("link command returned %T, want linkMsg", command())
	}
	_, _ = model.Update(message)
	if backend.linked.Application.Root != entry.Application.Root || backend.linked.Application.Name != "phoebe" {
		t.Fatalf("linked entry = %#v", backend.linked)
	}
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v after link, want closed", model.overlay)
	}
	linked, ok := model.selectedEntry()
	if !ok || !linked.Linked || linked.Application.Name != "phoebe" || linked.Application.URL() != "https://phoebe.localhost" {
		t.Fatalf("selected linked entry = %#v, exists %t", linked, ok)
	}
}

func TestDashboardTrustsRepositoryBeforeStarting(t *testing.T) {
	entry := Entry{
		Application:  catalog.Application{Name: "phoebe-ui", Domain: "phoebe-ui", Root: "/work/phoebe-ui", Commands: []catalog.Command{{Run: "pnpm dev"}}},
		Linked:       true,
		TrustSummary: "phoebe-ui wants to execute:\n  pnpm dev",
	}
	model := newDashboard(context.Background(), Options{Entries: []Entry{entry}})
	_, _ = model.Update(key("r"))
	if model.overlay != overlayTrust {
		t.Fatalf("overlay = %v, want trust", model.overlay)
	}
	if !strings.Contains(model.View(), "pnpm dev") {
		t.Fatalf("trust overlay does not show the command:\n%s", model.View())
	}
}

func key(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
