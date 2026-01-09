// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	tea "github.com/charmbracelet/bubbletea"
)

// ConcurrentDeployer orchestrates the concurrent deployment of multiple services
type ConcurrentDeployer struct {
	ctx               context.Context
	services          map[string]*azdext.ServiceConfig
	logsDir           string
	provisionLogPath  string
	ui                *tea.Program
	errChan           chan error
	wg                sync.WaitGroup
	activeDeployments atomic.Int32
	buildGate         *buildGate
	provision         *provisionState
	finalSummaryMu    sync.Mutex
	finalSummary      string
	debug             bool
}

// NewConcurrentDeployer creates a new concurrent deployer
func NewConcurrentDeployer(
	ctx context.Context,
	_ azdext.WorkflowServiceClient,
	services map[string]*azdext.ServiceConfig,
	ui *tea.Program,
	debug bool,
) (*ConcurrentDeployer, error) {
	// Create logs directory with unique timestamp
	timestamp := time.Now().Format("20060102-150405")
	logsDir := filepath.Join(".azure", "logs", "deploy", timestamp)
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Create provision log file path
	provisionLogPath := filepath.Join(logsDir, "provision.log")
	absProvisionLogPath, _ := filepath.Abs(provisionLogPath)

	return &ConcurrentDeployer{
		ctx:              ctx,
		services:         services,
		logsDir:          logsDir,
		provisionLogPath: absProvisionLogPath,
		ui:               ui,
		errChan:          make(chan error, len(services)),
		buildGate:        newBuildGate(),
		provision:        newProvisionState(),
		debug:            debug,
	}, nil
}

// Deploy runs the provision and deployment workflow
func (cd *ConcurrentDeployer) Deploy() error {
	// Start provision in background
	go cd.runProvision()

	// Start all service deployments
	cd.startServiceDeployments()

	// Wait for all deployments to complete
	go cd.waitForCompletion()

	// Run the UI and collect results
	return cd.collectResults()
}

// FinalSummary returns a plain-text summary suitable for printing after the TUI exits.
func (cd *ConcurrentDeployer) FinalSummary() string {
	cd.finalSummaryMu.Lock()
	defer cd.finalSummaryMu.Unlock()
	return cd.finalSummary
}

// runProvision executes the provision workflow
func (cd *ConcurrentDeployer) runProvision() {
	cd.ui.Send(provisionUpdateMsg{
		status:  "running",
		message: "Provisioning infrastructure...",
		logPath: cd.provisionLogPath,
	})

	// Create provision log file
	logFile, err := os.Create(cd.provisionLogPath)
	if err != nil {
		cd.ui.Send(provisionUpdateMsg{
			status:  "failed",
			message: "Failed to create provision log file",
			err:     err,
			logPath: cd.provisionLogPath,
		})
		cd.provision.Fail(err)
		return
	}
	defer logFile.Close()

	// Run azd provision as a subprocess to capture output
	args := []string{"provision"}
	if cd.debug {
		args = append(args, "--debug")
	}
	cmd := exec.CommandContext(cd.ctx, "azd", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Dir, _ = os.Getwd()
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "AZD_FORCE_TTY=false")

	err = cmd.Run()
	if err != nil {
		cd.ui.Send(provisionUpdateMsg{
			status:  "failed",
			message: "Provision failed",
			err:     err,
			logPath: cd.provisionLogPath,
		})
		cd.provision.Fail(err)
		return
	}

	cd.ui.Send(provisionUpdateMsg{
		status:  "completed",
		message: "Infrastructure provisioned",
		logPath: cd.provisionLogPath,
	})
	cd.provision.Succeed()
}

// startServiceDeployments starts all service deployment goroutines
func (cd *ConcurrentDeployer) startServiceDeployments() {
	for serviceName, service := range cd.services {
		cd.wg.Add(1)
		cd.activeDeployments.Add(1)

		deployer := newServiceDeployer(
			cd.ctx,
			serviceName,
			service,
			cd.logsDir,
			cd.ui,
			cd.buildGate,
			cd.provision,
			cd.errChan,
			cd.debug,
		)

		go func() {
			defer cd.wg.Done()
			defer cd.activeDeployments.Add(-1)
			deployer.Deploy()
		}()
	}
}

// waitForCompletion waits for all deployments and signals completion
func (cd *ConcurrentDeployer) waitForCompletion() {
	cd.wg.Wait()
	close(cd.errChan)
	cd.ui.Send(deploymentCompleteMsg{})
}

// collectResults runs the UI and collects deployment results
func (cd *ConcurrentDeployer) collectResults() error {
	finalModel, err := cd.ui.Run()
	if err != nil {
		return fmt.Errorf("UI error: %w", err)
	}

	if m, ok := finalModel.(deploymentModel); ok {
		// Only render summary if deployment was not cancelled by user
		if !m.cancelled {
			cd.finalSummaryMu.Lock()
			cd.finalSummary = renderPersistedSummary(&m)
			cd.finalSummaryMu.Unlock()
		}
	}

	// Check if user cancelled the deployment
	if m, ok := finalModel.(deploymentModel); ok && m.cancelled {
		return fmt.Errorf("deployment cancelled by user")
	}

	// Collect deployment errors
	var deployErrors []error
	for err := range cd.errChan {
		deployErrors = append(deployErrors, err)
	}

	// Check final model for errors
	if m, ok := finalModel.(deploymentModel); ok && m.err != nil {
		return m.err
	}

	if len(deployErrors) > 0 {
		return fmt.Errorf("%d service(s) failed to deploy", len(deployErrors))
	}

	return nil
}

// buildGate manages the Aspire build synchronization
type buildGate struct {
	firstAspire   bool
	firstAspireMu sync.Mutex
	openOnce      sync.Once
	failOnce      sync.Once
	readyCh       chan struct{}
	failCh        chan struct{}
	errMu         sync.Mutex
	err           error
}

func newBuildGate() *buildGate {
	return &buildGate{
		firstAspire: true,
		readyCh:     make(chan struct{}),
		failCh:      make(chan struct{}),
	}
}

// ClaimFirstAspire attempts to claim the first Aspire service slot
func (bg *buildGate) ClaimFirstAspire() bool {
	bg.firstAspireMu.Lock()
	defer bg.firstAspireMu.Unlock()

	if bg.firstAspire {
		bg.firstAspire = false
		return true
	}
	return false
}

// Open releases the build gate for other Aspire services.
func (bg *buildGate) Open() {
	bg.openOnce.Do(func() {
		close(bg.readyCh)
	})
}

// Fail marks the gate as failed and unblocks waiters with an error.
func (bg *buildGate) Fail(err error) {
	bg.failOnce.Do(func() {
		bg.errMu.Lock()
		bg.err = err
		bg.errMu.Unlock()
		close(bg.failCh)
	})
}

func (bg *buildGate) failure() error {
	bg.errMu.Lock()
	defer bg.errMu.Unlock()
	if bg.err == nil {
		return fmt.Errorf("Aspire build gate failed")
	}
	return bg.err
}

// Wait blocks until the gate is opened, fails, or the context is canceled.
func (bg *buildGate) Wait(ctx context.Context) error {
	select {
	case <-bg.readyCh:
		return nil
	case <-bg.failCh:
		return bg.failure()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// serviceDeployer handles deployment of a single service
type serviceDeployer struct {
	ctx         context.Context
	serviceName string
	service     *azdext.ServiceConfig
	logsDir     string
	ui          *tea.Program
	buildGate   *buildGate
	provision   *provisionState
	errChan     chan error
	logFile     *os.File
	logPath     string
	debug       bool
}

func newServiceDeployer(
	ctx context.Context,
	serviceName string,
	service *azdext.ServiceConfig,
	logsDir string,
	ui *tea.Program,
	buildGate *buildGate,
	provision *provisionState,
	errChan chan error,
	debug bool,
) *serviceDeployer {
	return &serviceDeployer{
		ctx:         ctx,
		serviceName: serviceName,
		service:     service,
		logsDir:     logsDir,
		ui:          ui,
		buildGate:   buildGate,
		provision:   provision,
		errChan:     errChan,
		debug:       debug,
	}
}

// Deploy executes the deployment for this service
func (sd *serviceDeployer) Deploy() {
	if err := sd.setup(); err != nil {
		sd.errChan <- err
		return
	}
	defer sd.cleanup()

	// Wait for provision to complete
	sd.updateStatus(StatusWaiting)
	if ok, err := sd.provision.Wait(); !ok {
		sd.handleError(fmt.Errorf("provision failed: %w", err))
		return
	}

	// Handle Aspire service synchronization
	isFirstAspire, err := sd.handleAspireGate()
	if err != nil {
		sd.handleError(err)
		return
	}

	// Run the deployment
	if err := sd.runDeployment(isFirstAspire); err != nil {
		sd.handleError(err)
		return
	}

	sd.updateStatus(StatusCompleted)
}

// setup prepares log files and initial state
func (sd *serviceDeployer) setup() error {
	logFileName := fmt.Sprintf("deploy-%s.log", sd.serviceName)
	logFilePath := filepath.Join(sd.logsDir, logFileName)
	sd.logPath, _ = filepath.Abs(logFilePath)

	var err error
	sd.logFile, err = os.Create(logFilePath)
	if err != nil {
		return fmt.Errorf("failed to create log file for service %s: %w", sd.serviceName, err)
	}

	return nil
}

// cleanup closes log files
func (sd *serviceDeployer) cleanup() {
	if sd.logFile != nil {
		sd.logFile.Close()
	}
}

// isAspireService determines if this is an Aspire service
func (sd *serviceDeployer) isAspireService() bool {
	return (sd.service.Host == "containerapp-dotnet" || sd.service.Host == "containerapp") &&
		(sd.service.Language == "dotnet" || sd.service.Language == "csharp")
}

// handleAspireGate manages Aspire build gate synchronization
func (sd *serviceDeployer) handleAspireGate() (bool, error) {
	if !sd.isAspireService() {
		sd.updateStatus(StatusDeploying)
		return false, nil
	}

	if sd.buildGate.ClaimFirstAspire() {
		sd.updateStatus(StatusDeploying)
		return true, nil
	}

	sd.updateStatus(StatusWaitingForGate)
	if err := sd.buildGate.Wait(sd.ctx); err != nil {
		return false, fmt.Errorf("waiting for Aspire build gate: %w", err)
	}
	sd.updateStatus(StatusDeploying)
	return false, nil
}

// runDeployment executes the actual deployment command
func (sd *serviceDeployer) runDeployment(isFirstAspire bool) error {
	var outputWriter io.Writer
	gateReleased := &atomic.Bool{}

	if isFirstAspire {
		outputWriter = newBuildGateWriter(sd.logFile, sd.buildGate, gateReleased, sd.serviceName)
	} else {
		outputWriter = sd.logFile
	}

	// #nosec G204 - serviceName is from validated azd context, not user input
	args := []string{"deploy", sd.serviceName}
	if sd.debug {
		args = append(args, "--debug")
	}
	// #nosec G204 - args constructed from validated inputs
	cmd := exec.CommandContext(sd.ctx, "azd", args...)
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter
	cmd.Dir, _ = os.Getwd()
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "AZD_FORCE_TTY=false")

	err := cmd.Run()
	if isFirstAspire {
		// If we never saw the marker, still make a decision so other Aspire services don't hang.
		if gateReleased.Load() {
			sd.buildGate.Open()
		} else if err != nil {
			sd.buildGate.Fail(fmt.Errorf("first Aspire service failed before parallel-safe point: %w", err))
		} else {
			// Deploy succeeded; allow others to proceed even if marker wasn't detected.
			sd.buildGate.Open()
		}
	}

	return err
}

type provisionState struct {
	done      chan struct{}
	succeeded atomic.Bool
	errMu     sync.Mutex
	err       error
}

func newProvisionState() *provisionState {
	return &provisionState{done: make(chan struct{})}
}

func (p *provisionState) Fail(err error) {
	p.errMu.Lock()
	p.err = err
	p.errMu.Unlock()
	close(p.done)
}

func (p *provisionState) Succeed() {
	p.succeeded.Store(true)
	close(p.done)
}

func (p *provisionState) Wait() (bool, error) {
	<-p.done
	if p.succeeded.Load() {
		return true, nil
	}
	p.errMu.Lock()
	defer p.errMu.Unlock()
	if p.err == nil {
		return false, fmt.Errorf("provision did not complete")
	}
	return false, p.err
}

func renderPersistedSummary(m *deploymentModel) string {
	var b strings.Builder

	b.WriteString("Concurx summary\n")
	b.WriteString("==============\n")

	if m.provisionStatus != "" {
		b.WriteString(fmt.Sprintf("Provision: %s\n", m.provisionStatus))
		if m.provisionLogPath != "" {
			b.WriteString(fmt.Sprintf("  Logs: %s\n", m.provisionLogPath))
		}
		if m.provisionErr != nil {
			b.WriteString(fmt.Sprintf("  Error: %v\n", m.provisionErr))
		}
	}

	b.WriteString("\nServices:\n")
	for _, name := range m.serviceOrder {
		svc := m.services[name]
		status := "unknown"
		switch svc.Status {
		case StatusWaiting:
			status = "waiting"
		case StatusWaitingForGate:
			status = "waiting-for-gate"
		case StatusDeploying:
			status = "deploying"
		case StatusCompleted:
			status = "completed"
		case StatusFailed:
			status = "failed"
		}

		b.WriteString(fmt.Sprintf("- %s: %s\n", name, status))
		if !svc.StartTime.IsZero() {
			end := svc.EndTime
			if end.IsZero() {
				end = time.Now()
			}
			b.WriteString(fmt.Sprintf("  Duration: %s\n", end.Sub(svc.StartTime).Round(time.Second)))
		}
		if svc.LogPath != "" {
			b.WriteString(fmt.Sprintf("  Logs: %s\n", svc.LogPath))
		}
		if svc.Error != nil {
			b.WriteString(fmt.Sprintf("  Error: %v\n", svc.Error))
		}
	}

	b.WriteString("\n")
	return b.String()
}

// updateStatus sends a status update to the UI
func (sd *serviceDeployer) updateStatus(status ServiceStatus) {
	sd.ui.Send(serviceUpdateMsg{
		name:    sd.serviceName,
		status:  status,
		logPath: sd.logPath,
	})
}

// handleError processes deployment errors
func (sd *serviceDeployer) handleError(err error) {
	deployErr := fmt.Errorf("failed to deploy service %s: %w", sd.serviceName, err)
	sd.errChan <- deployErr
	sd.ui.Send(serviceUpdateMsg{
		name:    sd.serviceName,
		status:  StatusFailed,
		logPath: sd.logPath,
		err:     deployErr,
	})
}
