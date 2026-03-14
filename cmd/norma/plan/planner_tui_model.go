package plancmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
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
type eventStreamClosedMsg struct{}
type questionStreamClosedMsg struct{}
type humanRequestMsg string
type planCompletedMsg string
type planFailedMsg string

const plannerIntroPrompt = "What do you want to build? Ctrl+C to exit."

type plannerModel struct {
	viewport    viewport.Model
	textinput   textinput.Model
	history     strings.Builder
	currentTurn strings.Builder
	renderer    *glamour.TermRenderer
	spinner     spinner.Model
	status      string

	// Channels for communication with the agent
	eventChan    <-chan *session.Event
	questionChan <-chan string
	responseChan chan<- string

	waitingForHuman bool
	waitingResponse bool
	completedRunMsg string
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
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	vp := viewport.New(100, 20)
	vp.Style = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(0, 1)

	return &plannerModel{
		viewport:     vp,
		textinput:    ti,
		renderer:     r,
		spinner:      sp,
		eventChan:    eventChan,
		questionChan: questionChan,
		responseChan: responseChan,
		onAbort:      onAbort,
		status:       "Starting planner...",
	}, nil
}

func (m *plannerModel) Init() tea.Cmd {
	m.history.WriteString(infoStyle.Render(plannerIntroPrompt + "\n\n"))
	m.status = "Waiting for agent updates..."
	m.updateViewport()
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
			return eventStreamClosedMsg{}
		}
		return eventMsg(ev)
	}
}

func (m *plannerModel) waitForQuestion() tea.Cmd {
	return func() tea.Msg {
		q, ok := <-m.questionChan
		if !ok {
			return questionStreamClosedMsg{}
		}
		return humanRequestMsg(q)
	}
}

func (m *plannerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
		spCmd tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.completedRunMsg != "" {
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
				_, _ = fmt.Fprintf(&m.history, "\n> %s\n", val)
				m.textinput.Reset()
				m.waitingForHuman = false
				m.waitingResponse = true
				m.status = "Message sent. Waiting for planner response..."
				m.updateViewport()
				return m, m.spinner.Tick
			}
		}

	case eventMsg:
		m.waitingResponse = false
		ev := (*session.Event)(msg)
		m.status = statusFromEvent(ev)
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

	case eventStreamClosedMsg, questionStreamClosedMsg:
		m.waitingForHuman = false
		m.waitingResponse = false
		return m, tea.Quit

	case humanRequestMsg:
		m.waitingResponse = false
		m.waitingForHuman = true
		m.status = "Waiting for your input..."
		question := strings.TrimSpace(string(msg))
		// Keep the fixed intro line only once in the viewport; render all other questions.
		if question != "" && question != plannerIntroPrompt {
			m.history.WriteString(questionStyle.Render(fmt.Sprintf("\n%s\n", question)))
		}
		m.updateViewport()
		return m, m.waitForQuestion()

	case planCompletedMsg:
		m.waitingForHuman = false
		m.waitingResponse = false
		m.completedRunMsg = strings.TrimSpace(string(msg))
		if m.completedRunMsg == "" {
			m.completedRunMsg = "Planner session complete."
		}
		m.status = "Planner session complete."
		var sb strings.Builder
		sb.WriteString("\n# Planner Session Complete\n\n")
		sb.WriteString(m.completedRunMsg)
		sb.WriteString("\n")
		rendered, _ := m.renderer.Render(sb.String())
		m.history.WriteString(rendered)
		m.updateViewport()
		return m, tea.Quit

	case planFailedMsg:
		m.waitingForHuman = false
		m.waitingResponse = false
		m.failedRunError = strings.TrimSpace(string(msg))
		m.status = "Planner failed."
		var sb strings.Builder
		sb.WriteString("\n# Planner Error\n\n")
		sb.WriteString(m.failedRunError)
		sb.WriteString("\n")
		rendered, _ := m.renderer.Render(sb.String())
		m.history.WriteString(rendered)
		m.updateViewport()
		return m, tea.Quit

	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6 // leave space for header and input
		m.textinput.Width = msg.Width
		m.updateViewport()

	case spinner.TickMsg:
		if m.waitingResponse {
			m.spinner, spCmd = m.spinner.Update(msg)
		}

	case error:
		m.err = msg
		m.status = "Error."
		m.waitingResponse = false
		return m, tea.Quit
	}

	if m.waitingForHuman {
		m.textinput, tiCmd = m.textinput.Update(msg)
	}

	// Only pass key messages to the viewport if we are NOT waiting for human input
	// to avoid conflicts between typing and viewport scrolling.
	// Non-key messages (like WindowSizeMsg) should always be passed.
	_, isKey := msg.(tea.KeyMsg)
	if !m.waitingForHuman || !isKey {
		m.viewport, vpCmd = m.viewport.Update(msg)
	}

	return m, tea.Batch(tiCmd, vpCmd, spCmd)
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
	case m.completedRunMsg != "":
		s += titleStyle.Render("Planner session complete. Press any key to exit.")
	case m.failedRunError != "":
		s += titleStyle.Render("Planner failed. Press any key to exit.")
	case m.waitingForHuman:
		s += m.textinput.View()
	default:
		status := m.currentStatus()
		if m.waitingResponse {
			status = fmt.Sprintf("%s %s", m.spinner.View(), status)
		}
		s += infoStyle.Render(status)
	}
	return s
}

func (m *plannerModel) currentStatus() string {
	status := strings.TrimSpace(m.status)
	if status == "" {
		return "Thinking..."
	}
	return status
}

// RunTUI runs the planner TUI and ensures terminal cleanup.
func RunTUI(prog *tea.Program) error {
	_, err := prog.Run()
	fmt.Println() // Ensure trailing newline on exit
	return err
}

func statusFromEvent(ev *session.Event) string {
	if ev == nil {
		return "Waiting for agent updates..."
	}
	if ev.Content != nil {
		for _, part := range ev.Content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall != nil {
				if title := toolTitle(part.FunctionCall.Args); title != "" {
					return fmt.Sprintf("Running tool: %s...", title)
				}
				if name := strings.TrimSpace(part.FunctionCall.Name); name != "" {
					return fmt.Sprintf("Running tool: %s...", name)
				}
				return "Running tool..."
			}
			if part.FunctionResponse != nil {
				if title := toolTitle(part.FunctionResponse.Response); title != "" {
					return fmt.Sprintf("Tool finished: %s", title)
				}
				if name := strings.TrimSpace(part.FunctionResponse.Name); name != "" {
					return fmt.Sprintf("Tool finished: %s", name)
				}
				return "Tool finished."
			}
		}
	}
	if ev.Partial {
		return "Agent is typing..."
	}
	if ev.TurnComplete {
		return "Waiting for next step..."
	}
	return "Thinking..."
}

func toolTitle(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	title, ok := payload["title"]
	if !ok {
		return ""
	}
	s, ok := title.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}
