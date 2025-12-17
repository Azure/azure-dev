// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ServiceStatus represents the current state of a service deployment
type ServiceStatus int

const (
	StatusWaiting ServiceStatus = iota
	StatusWaitingForGate
	StatusDeploying
	StatusCompleted
	StatusFailed
)

// ServiceState tracks the state of a single service deployment
type ServiceState struct {
	Name      string
	Status    ServiceStatus
	LogPath   string
	Error     error
	StartTime time.Time
	EndTime   time.Time
}

// deploymentModel is the Bubble Tea model for deployment visualization
type deploymentModel struct {
	services         map[string]*ServiceState
	serviceOrder     []string
	spinner          spinner.Model
	quitting         bool
	cancelled        bool // True if user cancelled with Ctrl+C
	err              error
	provisionStatus  string // "running", "completed", "failed"
	provisionMsg     string
	provisionLogPath string
	provisionErr     error
	cancel           context.CancelFunc // Cancel function to stop all deployments
}

// Messages that can be sent to the Bubble Tea program
type serviceUpdateMsg struct {
	name    string
	status  ServiceStatus
	logPath string
	err     error
}

type provisionUpdateMsg struct {
	status  string // "running", "completed", "failed"
	message string
	logPath string
	err     error
}

type deploymentCompleteMsg struct{}

type tickMsg time.Time

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39")).
			MarginBottom(1)

	statusWaitingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240"))

	statusWaitingGateStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214"))

	statusDeployingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("45"))

	statusCompletedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("46"))

	statusFailedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196"))

	logPathStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Italic(true)
)

func newDeploymentModel(serviceNames []string, cancel context.CancelFunc) deploymentModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	services := make(map[string]*ServiceState)
	for _, name := range serviceNames {
		services[name] = &ServiceState{
			Name:   name,
			Status: StatusWaiting,
		}
	}

	return deploymentModel{
		services:     services,
		serviceOrder: serviceNames,
		spinner:      s,
		cancel:       cancel,
	}
}

func (m deploymentModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m deploymentModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			m.cancelled = true
			// Cancel context to stop all running deployments
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}

	case provisionUpdateMsg:
		m.provisionStatus = msg.status
		m.provisionMsg = msg.message
		if msg.logPath != "" {
			m.provisionLogPath = msg.logPath
		}
		if msg.err != nil {
			m.provisionErr = msg.err
		}
		return m, nil

	case serviceUpdateMsg:
		if svc, ok := m.services[msg.name]; ok {
			svc.Status = msg.status
			if msg.logPath != "" {
				svc.LogPath = msg.logPath
			}
			if msg.err != nil {
				svc.Error = msg.err
			}
			if msg.status == StatusDeploying && svc.StartTime.IsZero() {
				svc.StartTime = time.Now()
			}
			if (msg.status == StatusCompleted || msg.status == StatusFailed) && svc.EndTime.IsZero() {
				svc.EndTime = time.Now()
			}
		}
		return m, nil

	case deploymentCompleteMsg:
		m.quitting = true
		return m, tea.Quit

	case tickMsg:
		// Continue ticking for spinner animation
		return m, tickCmd()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case error:
		m.err = msg
		m.quitting = true
		return m, tea.Quit
	}

	return m, nil
}

func (m deploymentModel) View() string {
	if m.err != nil {
		return statusFailedStyle.Render(fmt.Sprintf("Error: %v\n", m.err))
	}

	if m.quitting {
		return m.renderFinalView()
	}

	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render("🚀 Azure Developer CLI - ConcurX"))
	b.WriteString("\n\n")

	// Provision status
	if m.provisionStatus != "" {
		switch m.provisionStatus {
		case "running":
			msg := "Provisioning infrastructure..."
			b.WriteString(fmt.Sprintf("  %s %s\n", m.spinner.View(), statusDeployingStyle.Render(msg)))
			if m.provisionLogPath != "" {
				b.WriteString(fmt.Sprintf("    %s\n\n", logPathStyle.Render(fmt.Sprintf("Logs: %s", m.provisionLogPath))))
			} else {
				b.WriteString("\n")
			}
		case "completed":
			b.WriteString(fmt.Sprintf("  %s %s\n\n", "✓", statusCompletedStyle.Render("Infrastructure provisioned")))
		case "failed":
			b.WriteString(fmt.Sprintf("  %s %s\n", "✗", statusFailedStyle.Render("Provision failed")))
			if m.provisionErr != nil {
				b.WriteString(fmt.Sprintf("    %s\n", statusFailedStyle.Render(m.provisionErr.Error())))
			}
			if m.provisionLogPath != "" {
				b.WriteString(fmt.Sprintf("    %s\n\n", logPathStyle.Render(fmt.Sprintf("Logs: %s", m.provisionLogPath))))
			} else {
				b.WriteString("\n")
			}
		}
	}

	// Only show deployment section if provision is completed
	if m.provisionStatus == "completed" || len(m.services) > 0 {
		b.WriteString(fmt.Sprintf("Deploying %d services in parallel:\n\n", len(m.services)))
	}

	// Service statuses
	for _, name := range m.serviceOrder {
		svc := m.services[name]
		b.WriteString(m.renderServiceStatus(svc))
		b.WriteString("\n")
	}

	// Help text
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render("Press q or Ctrl+C to quit"))

	return b.String()
}

func (m deploymentModel) renderServiceStatus(svc *ServiceState) string {
	var icon, status string
	var statusStyle lipgloss.Style

	switch svc.Status {
	case StatusWaiting:
		icon = "⏳"
		status = "Waiting to deploy"
		statusStyle = statusWaitingStyle
	case StatusWaitingForGate:
		icon = "🔒"
		status = "Waiting for first Aspire service to build..."
		statusStyle = statusWaitingGateStyle
	case StatusDeploying:
		icon = m.spinner.View()
		elapsed := time.Since(svc.StartTime)
		status = fmt.Sprintf("Deploying... (%s)", formatDuration(elapsed))
		statusStyle = statusDeployingStyle
	case StatusCompleted:
		icon = "✓"
		duration := svc.EndTime.Sub(svc.StartTime)
		status = fmt.Sprintf("Completed (%s)", formatDuration(duration))
		statusStyle = statusCompletedStyle
	case StatusFailed:
		icon = "✗"
		status = fmt.Sprintf("Failed: %v", svc.Error)
		statusStyle = statusFailedStyle
	}

	result := fmt.Sprintf("  %s %s", icon, statusStyle.Render(fmt.Sprintf("%-30s", svc.Name)))
	result += " " + statusStyle.Render(status)

	if svc.LogPath != "" && (svc.Status == StatusDeploying || svc.Status == StatusFailed) {
		result += "\n    " + logPathStyle.Render(fmt.Sprintf("Logs: %s", svc.LogPath))
	}

	return result
}

func (m deploymentModel) renderFinalView() string {
	var b strings.Builder

	// Count results
	completed := 0
	failed := 0
	for _, svc := range m.services {
		if svc.Status == StatusCompleted {
			completed++
		} else if svc.Status == StatusFailed {
			failed++
		}
	}

	b.WriteString("\n")
	b.WriteString(titleStyle.Render("Deployment Summary"))
	b.WriteString("\n\n")

	if failed > 0 {
		b.WriteString(statusFailedStyle.Render(fmt.Sprintf("✗ %d service(s) failed", failed)))
		b.WriteString("\n")
		b.WriteString(statusCompletedStyle.Render(fmt.Sprintf("✓ %d service(s) completed", completed)))
		b.WriteString("\n\n")

		// List failed services
		b.WriteString("Failed services:\n")
		for _, name := range m.serviceOrder {
			svc := m.services[name]
			if svc.Status == StatusFailed {
				b.WriteString(fmt.Sprintf("  - %s: %v\n", name, svc.Error))
				if svc.LogPath != "" {
					b.WriteString(fmt.Sprintf("    Logs: %s\n", svc.LogPath))
				}
			}
		}
	} else {
		b.WriteString(statusCompletedStyle.Render(fmt.Sprintf("✓ All %d services deployed successfully!", completed)))
		b.WriteString("\n")
	}

	return b.String()
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", minutes, seconds)
}
