package planner

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/adk/session"
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Bold(true).
			Padding(0, 1)

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)

	questionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Bold(true).
			MarginLeft(2)
)

type eventMsg *session.Event
type humanRequestMsg string
type planFinishedMsg Decomposition
type planFailedMsg string

type plannerModel struct {
	viewport    viewport.Model
	textinput   textinput.Model
	history     strings.Builder
	currentTurn strings.Builder
	renderer    *glamour.TermRenderer

	// Channels for communication with the agent
	eventChan    <-chan *session.Event
	questionChan <-chan string
	responseChan chan<- string

	waitingForHuman bool
	finishedPlan    *Decomposition
	failedRunError  string
	err             error
	onAbort         func()
}

func newPlannerModel(
	eventChan <-chan *session.Event,
	questionChan <-chan string,
	responseChan chan<- string,
	onAbort func(),
) (*plannerModel, error) {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)
	if err != nil {
		return nil, err
	}

	ti := textinput.New()
	ti.Placeholder = "Type your answer..."
	ti.Focus()

	vp := viewport.New(100, 20)
	vp.Style = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(0, 1)

	return &plannerModel{
		viewport:     vp,
		textinput:    ti,
		renderer:     r,
		eventChan:    eventChan,
		questionChan: questionChan,
		responseChan: responseChan,
		onAbort:      onAbort,
	}, nil
}

func (m *plannerModel) Init() tea.Cmd {
	return tea.Batch(
		m.waitForEvent(),
		m.waitForQuestion(),
		textinput.Blink,
	)
}

func (m *plannerModel) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.eventChan
		if !ok {
			return nil
		}
		return eventMsg(ev)
	}
}

func (m *plannerModel) waitForQuestion() tea.Cmd {
	return func() tea.Msg {
		q, ok := <-m.questionChan
		if !ok {
			return nil
		}
		return humanRequestMsg(q)
	}
}

func (m *plannerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.finishedPlan != nil {
			return m, tea.Quit
		}
		if m.failedRunError != "" {
			return m, tea.Quit
		}
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			if m.onAbort != nil {
				m.onAbort()
			}
			return m, tea.Quit
		case tea.KeyEnter:
			if m.waitingForHuman {
				val := m.textinput.Value()
				if val == "" {
					val = "No answer provided."
				}
				m.responseChan <- val
				m.history.WriteString(fmt.Sprintf("\n> %s\n", val))
				m.textinput.Reset()
				m.waitingForHuman = false
				m.updateViewport()
				return m, nil
			}
		}

	case eventMsg:
		ev := (*session.Event)(msg)
		if ev.Content != nil {
			for _, part := range ev.Content.Parts {
				if part.Text != "" {
					m.currentTurn.WriteString(part.Text)
				}
			}
		}
		// Render when turn is complete or if it's not a partial response
		if !ev.Partial || ev.TurnComplete {
			if m.currentTurn.Len() > 0 {
				rendered, _ := m.renderer.Render(m.currentTurn.String())
				m.history.WriteString(rendered)
				m.currentTurn.Reset()
				m.updateViewport()
			}
		}
		return m, m.waitForEvent()

	case humanRequestMsg:
		m.waitingForHuman = true
		m.history.WriteString(questionStyle.Render(fmt.Sprintf("\n[PLANNER QUESTION]: %s\n", string(msg))))
		m.updateViewport()
		return m, nil

	case planFinishedMsg:
		plan := Decomposition(msg)
		m.finishedPlan = &plan
		m.waitingForHuman = false

		// Render final plan into history
		var sb strings.Builder
		sb.WriteString("\n# Final Plan Generated and Persisted\n\n")
		sb.WriteString(fmt.Sprintf("## Epic: %s\n\n%s\n\n", plan.Epic.Title, plan.Epic.Description))

		for _, f := range plan.Features {
			sb.WriteString(fmt.Sprintf("### Feature: %s\n\n%s\n\n", f.Title, f.Description))
			for _, t := range f.Tasks {
				sb.WriteString(fmt.Sprintf("#### Task: %s\n", t.Title))
				sb.WriteString(fmt.Sprintf("- **Objective:** %s\n", t.Objective))
				sb.WriteString(fmt.Sprintf("- **Artifact:** %s\n", t.Artifact))
				sb.WriteString("- **Verify:**\n")
				for _, v := range t.Verify {
					sb.WriteString(fmt.Sprintf("  - %s\n", v))
				}
				if t.Notes != "" {
					sb.WriteString(fmt.Sprintf("- **Notes:** %s\n", t.Notes))
				}
				sb.WriteString("\n")
			}
		}

		rendered, _ := m.renderer.Render(sb.String())
		m.history.WriteString(rendered)
		m.updateViewport()
		return m, nil

	case planFailedMsg:
		m.waitingForHuman = false
		m.failedRunError = strings.TrimSpace(string(msg))
		var sb strings.Builder
		sb.WriteString("\n# Planner Error\n\n")
		sb.WriteString(m.failedRunError)
		sb.WriteString("\n")
		rendered, _ := m.renderer.Render(sb.String())
		m.history.WriteString(rendered)
		m.updateViewport()
		return m, nil

	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6 // leave space for header and input
		m.textinput.Width = msg.Width
		m.updateViewport()

	case error:
		m.err = msg
		return m, tea.Quit
	}

	if m.waitingForHuman {
		m.textinput, tiCmd = m.textinput.Update(msg)
	}
	m.viewport, vpCmd = m.viewport.Update(msg)

	return m, tea.Batch(tiCmd, vpCmd)
}

func (m *plannerModel) updateViewport() {
	m.viewport.SetContent(m.history.String())
	m.viewport.GotoBottom()
}

func (m *plannerModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	s := fmt.Sprintf("%s\n\n%s\n\n", titleStyle.Render("Norma Planner"), m.viewport.View())
	switch {
	case m.finishedPlan != nil:
		s += titleStyle.Render("Plan persisted! Press any key to exit.")
	case m.failedRunError != "":
		s += titleStyle.Render("Planner failed. Press any key to exit.")
	case m.waitingForHuman:
		s += m.textinput.View()
	default:
		s += infoStyle.Render("Thinking...")
	}
	return s
}
