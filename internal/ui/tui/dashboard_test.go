package tui

import (
	"strings"
	"testing"

	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/charmbracelet/bubbles/list"
)

func TestDashboardItemsShowsOneClearSetupAction(t *testing.T) {
	items, runnable := dashboardItems([]catalog.Application{{
		Name: "cassie",
		Root: "/work/cassie",
	}})
	if runnable != 0 {
		t.Fatalf("runnable applications = %d, want 0", runnable)
	}
	if len(items) != 3 {
		t.Fatalf("dashboard items = %d, want 3", len(items))
	}
	setup := items[0].(item)
	if setup.title != "Set up this directory" || setup.root != "/work/cassie" {
		t.Fatalf("unexpected setup item: %#v", setup)
	}
}

func TestDashboardIncludesBrand(t *testing.T) {
	model := dashboard{list: listForTest(), runnable: 0}
	view := model.View()
	if !strings.Contains(view, "C A S S I E") || !strings.Contains(view, "No configured applications yet") {
		t.Fatalf("dashboard does not include brand and empty state:\n%s", view)
	}
}

func listForTest() list.Model {
	return list.New(nil, list.NewDefaultDelegate(), 72, 10)
}
