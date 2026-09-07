package tui

import (
	"fmt"

	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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
	selected Selection
}

type Selection struct {
	Action Action
	Root   string
	Name   string
}

func Choose(apps []catalog.Application) (Selection, error) {
	items := make([]list.Item, 0, len(apps)+3)
	for _, app := range apps {
		if len(app.Commands) > 0 {
			items = append(items, item{title: "Run " + app.Name, description: app.URL(), action: ActionRun, root: app.Root, name: app.Name})
		} else {
			items = append(items, item{title: "Link " + app.Name, description: app.Root, action: ActionLink, root: app.Root, name: app.Name})
		}
	}
	items = append(items,
		item{title: "Link an app", description: "Configure the current directory", action: ActionLink},
		item{title: "Status", description: "Recent development sessions", action: ActionStatus},
		item{title: "Doctor", description: "Check local dependencies", action: ActionDoctor},
	)

	l := list.New(items, list.NewDefaultDelegate(), 72, 16)
	l.Title = "cassie"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).PaddingLeft(1)
	model := dashboard{list: l}
	program := tea.NewProgram(model)
	result, err := program.Run()
	if err != nil {
		return Selection{}, err
	}
	return result.(dashboard).selected, nil
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
		m.list.SetSize(message.Width, message.Height-3)
	}
	var command tea.Cmd
	m.list, command = m.list.Update(message)
	return m, command
}

func (m dashboard) View() string {
	summary := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).PaddingLeft(1).
		Render(fmt.Sprintf("%d application(s) discovered", len(m.list.Items())-3))
	return summary + "\n" + m.list.View()
}
