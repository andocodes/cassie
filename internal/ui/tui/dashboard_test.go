package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/andocodes/cassie/internal/domain/catalog"
	runtimeDomain "github.com/andocodes/cassie/internal/domain/runtime"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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
