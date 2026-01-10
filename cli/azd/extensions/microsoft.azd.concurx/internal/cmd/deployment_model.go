// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
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

// viewMode represents the current view
type viewMode int

const (
	viewDeployment viewMode = iota
	viewLogs
)

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
	// Logs view state
	viewMode    viewMode
	selectedTab int
	tabNames    []string // "provision" followed by service names
	logContents map[string]string
	viewport    viewport.Model
	width       int
	height      int
	ready       bool
	autoRefresh bool // Auto-refresh logs when enabled
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

type logRefreshMsg time.Time

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

	// Initialize tab names: provision first, then services
	tabNames := make([]string, 0, len(serviceNames)+1)
	tabNames = append(tabNames, "provision")
	tabNames = append(tabNames, serviceNames...)

	// Create viewport with default key mappings
	vp := viewport.New(120, 25)

	return deploymentModel{
		services:     services,
		serviceOrder: serviceNames,
		spinner:      s,
		cancel:       cancel,
		viewMode:     viewDeployment,
		selectedTab:  0,
		tabNames:     tabNames,
		logContents:  make(map[string]string),
		viewport:     vp,
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

func logRefreshCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*500, func(t time.Time) tea.Msg {
		return logRefreshMsg(t)
	})
}

func (m deploymentModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.ready = true
		}
		// Update viewport size when in logs view
		if m.viewMode == viewLogs {
			headerHeight := 7 // title + instructions + tabs + spacing
			m.viewport.Width = msg.Width - 4
			m.viewport.Height = msg.Height - headerHeight
			m.updateViewportContent()
		}
		return m, nil

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
		case "l", "L":
			// Toggle to logs view
			if m.viewMode == viewDeployment {
				m.viewMode = viewLogs
				m.refreshLogContents()
				// Update viewport size based on current terminal size
				if m.ready {
					headerHeight := 7
					m.viewport.Width = m.width - 4
					m.viewport.Height = m.height - headerHeight
				}
				m.updateViewportContent()
			}
			return m, nil
		case "b", "B":
			// Back to deployment view
			if m.viewMode == viewLogs {
				m.viewMode = viewDeployment
			}
			return m, nil
		case "left":
			// Previous tab
			if m.viewMode == viewLogs && m.selectedTab > 0 {
				m.selectedTab--
				m.refreshLogContents()
				m.updateViewportContent()
			}
			return m, nil
		case "right":
			// Next tab
			if m.viewMode == viewLogs && m.selectedTab < len(m.tabNames)-1 {
				m.selectedTab++
				m.refreshLogContents()
				m.updateViewportContent()
			}
			return m, nil
		case "i", "I":
			// Jump to beginning of log
			if m.viewMode == viewLogs {
				m.viewport.GotoTop()
			}
			return m, nil
		case "o", "O":
			// Open log file in default editor
			if m.viewMode == viewLogs {
				m.openCurrentLogFile()
			}
			return m, nil
		case "a", "A":
			// Toggle auto-refresh
			if m.viewMode == viewLogs {
				m.autoRefresh = !m.autoRefresh
				if m.autoRefresh {
					// Start refresh ticker
					return m, logRefreshCmd()
				}
			}
			return m, nil
		}

		// Handle viewport scrolling when in logs view
		if m.viewMode == viewLogs {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
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

	case logRefreshMsg:
		// Auto-refresh logs if enabled
		if m.viewMode == viewLogs && m.autoRefresh {
			// Check if we're at the bottom before refreshing
			atBottom := m.viewport.AtBottom()
			m.refreshLogContents()
			// Update viewport content
			if m.selectedTab >= 0 && m.selectedTab < len(m.tabNames) {
				tabName := m.tabNames[m.selectedTab]
				if content, ok := m.logContents[tabName]; ok {
					m.viewport.SetContent(content)
					// Stay at bottom if we were there
					if atBottom {
						m.viewport.GotoBottom()
					}
				}
			}
			// Continue refresh ticker
			return m, logRefreshCmd()
		}
		return m, nil

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

	if m.viewMode == viewLogs {
		return m.renderLogsView()
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
		Render("Press L to see logs • Press q or Ctrl+C to quit"))

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

// refreshLogContents reads log files and updates the log contents map
func (m *deploymentModel) refreshLogContents() {
	// Only refresh the currently selected tab to avoid reading all files
	if m.selectedTab < 0 || m.selectedTab >= len(m.tabNames) {
		return
	}

	tabName := m.tabNames[m.selectedTab]
	var logPath string

	if tabName == "provision" {
		logPath = m.provisionLogPath
	} else if svc, ok := m.services[tabName]; ok {
		logPath = svc.LogPath
	}

	if logPath == "" {
		m.logContents[tabName] = "No log file available yet"
		return
	}

	content, err := readLogFile(logPath)
	if err != nil {
		m.logContents[tabName] = fmt.Sprintf("Error reading log file: %v", err)
		return
	}

	m.logContents[tabName] = content
}

// readLogFile reads the entire content of a log file
func readLogFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// renderLogsView renders the logs viewer with tabs
func (m deploymentModel) renderLogsView() string {
	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render("📋 Deployment Logs"))
	b.WriteString("\n\n")

	// Instructions with auto-refresh status
	autoRefreshStatus := ""
	if m.autoRefresh {
		autoRefreshStatus = lipgloss.NewStyle().
			Foreground(lipgloss.Color("46")).
			Bold(true).
			Render(" [AUTO-REFRESH ON]")
	}
	helpText := "← → tabs • ↑↓ scroll • I top • A toggle refresh • O open • B back • q quit"
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(helpText))
	b.WriteString(autoRefreshStatus)
	b.WriteString("\n\n")

	// Render tabs
	b.WriteString(m.renderTabs())
	b.WriteString("\n\n")

	// Render viewport with scrollable log content
	b.WriteString(m.viewport.View())

	return b.String()
}

// updateViewportContent updates the viewport with current log content
func (m *deploymentModel) updateViewportContent() {
	if m.selectedTab < 0 || m.selectedTab >= len(m.tabNames) {
		return
	}

	tabName := m.tabNames[m.selectedTab]
	content, ok := m.logContents[tabName]
	if !ok || content == "" {
		content = "Loading logs..."
	}

	// Remember if we were at bottom before updating
	atBottom := m.viewport.AtBottom()
	m.viewport.SetContent(content)

	// Scroll to bottom by default to show latest logs, or maintain position
	if atBottom || !m.autoRefresh {
		m.viewport.GotoBottom()
	}
}

// renderTabs renders the tab navigation bar
func (m deploymentModel) renderTabs() string {
	var tabs []string

	activeTabStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("39")).
		Padding(0, 2)

	inactiveTabStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Background(lipgloss.Color("236")).
		Padding(0, 2)

	for i, tabName := range m.tabNames {
		var style lipgloss.Style
		if i == m.selectedTab {
			style = activeTabStyle
		} else {
			style = inactiveTabStyle
		}
		tabs = append(tabs, style.Render(tabName))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

// openCurrentLogFile opens the current log file in the default editor
func (m *deploymentModel) openCurrentLogFile() {
	if m.selectedTab < 0 || m.selectedTab >= len(m.tabNames) {
		return
	}

	tabName := m.tabNames[m.selectedTab]
	var logPath string

	if tabName == "provision" {
		logPath = m.provisionLogPath
	} else if svc, ok := m.services[tabName]; ok {
		logPath = svc.LogPath
	}

	if logPath == "" {
		return
	}

	// Try to open in VS Code first
	if m.openInVSCode(logPath) {
		return
	}

	// Fallback: Open file with default application based on OS
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// #nosec G204 - logPath comes from internally controlled log file paths
		cmd = exec.Command("open", logPath)
	case "windows":
		// #nosec G204 - logPath comes from internally controlled log file paths
		cmd = exec.Command("cmd", "/c", "start", logPath)
	default: // linux, bsd, etc.
		// #nosec G204 - logPath comes from internally controlled log file paths
		cmd = exec.Command("xdg-open", logPath)
	}

	// Run asynchronously - we don't care about errors here
	_ = cmd.Start()
}

// openInVSCode attempts to open a file in VS Code, returns true if successful
func (m *deploymentModel) openInVSCode(filePath string) bool {
	// Determine the VS Code command name based on OS
	codeCmd := "code"
	if runtime.GOOS == "windows" {
		codeCmd = "code.exe"
	}

	// Check if VS Code is available in PATH
	_, err := exec.LookPath(codeCmd)
	if err != nil {
		return false
	}

	// Try to open the file in VS Code
	// #nosec G204 - filePath comes from internally controlled log file paths
	cmd := exec.Command(codeCmd, filePath)
	err = cmd.Run()
	return err == nil
}
