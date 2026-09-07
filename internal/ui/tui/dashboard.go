package tui

import (
	"fmt"

	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const lion = `      ▄▄   ▄▄
    ▄█████████▄
   ██╭───────╮██
  ███│ ●   ● │███
  ███│   ◆   │███
   ██╰─╲___╱─╯██
     ▀███████▀`

type Action string

const (
	ActionNone   Action = ""
	ActionRun    Action = "run"
	ActionLink   Action = "link"
	ActionStatus Action = "status"
	ActionDoctor Action = "doctor"
)

type item struct {
	title       string
	description string
	action      Action
	root        string
	name        string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.description }
func (i item) FilterValue() string { return i.title }

type dashboard struct {
	list     list.Model
	runnable int
	selected Selection
}

type Selection struct {
	Action Action
	Root   string
	Name   string
}

func Choose(apps []catalog.Application) (Selection, error) {
	items, runnable := dashboardItems(apps)

	l := list.New(items, list.NewDefaultDelegate(), 72, 21)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	model := dashboard{list: l, runnable: runnable}
	program := tea.NewProgram(model)
	result, err := program.Run()
	if err != nil {
		return Selection{}, err
	}
	return result.(dashboard).selected, nil
}

func dashboardItems(apps []catalog.Application) ([]list.Item, int) {
	items := make([]list.Item, 0, len(apps)+3)
	runnable := 0
	for _, app := range apps {
		if len(app.Commands) > 0 {
			runnable++
			items = append(items, item{title: "Run " + app.Name, description: app.URL(), action: ActionRun, root: app.Root, name: app.Name})
		} else if len(apps) > 1 {
			items = append(items, item{title: "Set up " + app.Name, description: app.Root, action: ActionLink, root: app.Root, name: app.Name})
		}
	}
	linkTitle := "Link an app"
	linkDescription := "Configure the current directory"
	linkRoot := ""
	if len(apps) == 1 && runnable == 0 {
		linkTitle = "Set up this directory"
		linkDescription = apps[0].Root
		linkRoot = apps[0].Root
	}
	items = append(items,
		item{title: linkTitle, description: linkDescription, action: ActionLink, root: linkRoot},
		item{title: "Status", description: "Recent development sessions", action: ActionStatus},
		item{title: "Doctor", description: "Check local dependencies", action: ActionDoctor},
	)
	return items, runnable
}

func (m dashboard) Init() tea.Cmd { return nil }

func (m dashboard) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.KeyMsg:
		switch message.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			if selected, ok := m.list.SelectedItem().(item); ok {
				m.selected = Selection{Action: selected.action, Root: selected.root, Name: selected.name}
			}
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.list.SetSize(message.Width, max(message.Height-11, 5))
	}
	var command tea.Cmd
	m.list, command = m.list.Update(message)
	return m, command
}

func (m dashboard) View() string {
	summary := "No configured applications yet"
	if m.runnable == 1 {
		summary = "1 configured application"
	} else if m.runnable > 1 {
		summary = fmt.Sprintf("%d configured applications", m.runnable)
	}
	mane := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")).Render(lion)
	name := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Render("     C A S S I E")
	status := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(summary)
	return mane + "\n" + name + "\n\n" + status + "\n" + m.list.View()
}
