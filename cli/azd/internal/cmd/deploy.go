// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type DeployFlags struct {
	ServiceName string
	All         bool
	Timeout     int
	fromPackage string
	flagSet     *pflag.FlagSet
	global      *internal.GlobalCommandOptions
	*internal.EnvFlag
}

const defaultDeployTimeoutSeconds = 1200

func (d *DeployFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	d.BindNonCommon(local, global)
	d.bindCommon(local, global)
}

func (d *DeployFlags) BindNonCommon(
	local *pflag.FlagSet,
	global *internal.GlobalCommandOptions) {
	local.StringVar(
		&d.ServiceName,
		"service",
		"",
		//nolint:lll
		"Deploys a specific service (when the string is unspecified, all services that are listed in the "+azdcontext.ProjectFileName+" file are deployed).",
	)
	//deprecate:flag hide --service
	_ = local.MarkHidden("service")
	d.global = global
}

func (d *DeployFlags) bindCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	d.EnvFlag = &internal.EnvFlag{}
	d.EnvFlag.Bind(local, global)
	d.flagSet = local

	local.BoolVar(
		&d.All,
		"all",
		false,
		"Deploys all services that are listed in "+azdcontext.ProjectFileName,
	)
	local.StringVar(
		&d.fromPackage,
		"from-package",
		"",
		//nolint:lll
		"Deploys the packaged service located at the provided path. Supports zipped file packages (file path) or container images (image tag).",
	)
	local.IntVar(
		&d.Timeout,
		"timeout",
		defaultDeployTimeoutSeconds,
		fmt.Sprintf(
			"Maximum time in seconds for azd to wait for each service deployment. This stops azd from waiting "+
				"but does not cancel the Azure-side deployment. (default: %d)",
			defaultDeployTimeoutSeconds,
		),
	)
}

func (d *DeployFlags) SetCommon(envFlag *internal.EnvFlag) {
	d.EnvFlag = envFlag
}

func NewDeployFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *DeployFlags {
	flags := &DeployFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func NewDeployFlagsFromEnvAndOptions(envFlag *internal.EnvFlag, global *internal.GlobalCommandOptions) *DeployFlags {
	return &DeployFlags{
		Timeout: defaultDeployTimeoutSeconds,
		EnvFlag: envFlag,
		global:  global,
	}
}

func (d *DeployFlags) timeoutChanged() bool {
	if d.flagSet == nil {
		return false
	}

	timeoutFlag := d.flagSet.Lookup("timeout")
	return timeoutFlag != nil && timeoutFlag.Changed
}

func NewDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy <service>",
		Short: "Deploy your project code to Azure.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
}

type DeployAction struct {
	flags               *DeployFlags
	args                []string
	projectConfig       *project.ProjectConfig
	azdCtx              *azdcontext.AzdContext
	env                 *environment.Environment
	envManager          environment.Manager
	projectManager      project.ProjectManager
	serviceManager      project.ServiceManager
	resourceManager     project.ResourceManager
	accountManager      account.Manager
	azCli               *azapi.AzureClient
	portalUrlBase       string
	formatter           output.Formatter
	writer              io.Writer
	console             input.Console
	commandRunner       exec.CommandRunner
	alphaFeatureManager *alpha.FeatureManager
	importManager       *project.ImportManager
	progressTracker     *deployProgressTracker // set at runtime when using parallel deployment graph
}

func NewDeployAction(
	flags *DeployFlags,
	args []string,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	serviceManager project.ServiceManager,
	resourceManager project.ResourceManager,
	azdCtx *azdcontext.AzdContext,
	environment *environment.Environment,
	envManager environment.Manager,
	accountManager account.Manager,
	cloud *cloud.Cloud,
	azCli *azapi.AzureClient,
	commandRunner exec.CommandRunner,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	alphaFeatureManager *alpha.FeatureManager,
	importManager *project.ImportManager,
) actions.Action {
	return &DeployAction{
		flags:               flags,
		args:                args,
		projectConfig:       projectConfig,
		azdCtx:              azdCtx,
		env:                 environment,
		envManager:          envManager,
		projectManager:      projectManager,
		serviceManager:      serviceManager,
		resourceManager:     resourceManager,
		accountManager:      accountManager,
		portalUrlBase:       cloud.PortalUrlBase,
		azCli:               azCli,
		formatter:           formatter,
		writer:              writer,
		console:             console,
		commandRunner:       commandRunner,
		alphaFeatureManager: alphaFeatureManager,
		importManager:       importManager,
	}
}

type DeploymentResult struct {
	Timestamp time.Time                               `json:"timestamp"`
	Services  map[string]*project.ServiceDeployResult `json:"services"`
}

func (da *DeployAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	targetServiceName := da.flags.ServiceName
	if len(da.args) == 1 {
		targetServiceName = da.args[0]
	}

	if da.env.GetSubscriptionId() == "" {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrInfraNotProvisioned,
			Suggestion: "Run 'azd provision' to set up infrastructure before deploying.",
		}
	}

	targetServiceName, err := getTargetServiceName(
		ctx,
		da.projectManager,
		da.importManager,
		da.projectConfig,
		string(project.ServiceEventDeploy),
		targetServiceName,
		da.flags.All,
	)
	if err != nil {
		return nil, err
	}

	if da.flags.All && da.flags.fromPackage != "" {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrFromPackageWithAll,
			Suggestion: "Use 'azd deploy <service> --from-package <path>' to target a specific service.",
		}
	}

	if targetServiceName == "" && da.flags.fromPackage != "" {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrFromPackageNoService,
			Suggestion: "Use 'azd deploy <service> --from-package <path>' to target a specific service.",
		}
	}

	if err := da.projectManager.Initialize(ctx, da.projectConfig); err != nil {
		return nil, err
	}

	if err := da.projectManager.EnsureServiceTargetTools(ctx, da.projectConfig, func(svc *project.ServiceConfig) bool {
		return targetServiceName == "" || svc.Name == targetServiceName
	}); err != nil {
		return nil, err
	}

	// Command title
	da.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Deploying services (azd deploy)",
	})

	startTime := time.Now()

	stableServices, err := da.importManager.ServiceStableFiltered(ctx, da.projectConfig, targetServiceName, da.env.Getenv)
	if err != nil {
		return nil, err
	}

	// Always deploy through the service execution graph. The graph handles
	// any service count (including N=1) with a uniform progress tracker
	// and the same package → publish → deploy step topology.
	return da.deployServicesGraph(ctx, stableServices, startTime)
}

// deployServicesGraph builds an execution graph of service deployments and runs them in
// parallel via the execution graph scheduler. Services that share a non-empty
// [serviceGraphOptions.buildGateKey] serialize on the first one to act as a
// shared build gate; today that policy only fires for Aspire services
// (DotNetContainerApp != nil), which share a single .NET AppHost build. Every
// other service runs fully in parallel with no inter-service edges.
func (da *DeployAction) deployServicesGraph(
	ctx context.Context,
	stableServices []*project.ServiceConfig,
	startTime time.Time,
) (*actions.ActionResult, error) {
	deployTimeout, err := da.resolveDeployTimeout()
	if err != nil {
		return nil, err
	}

	// Wrap console for thread-safe output during parallel deployment.
	// Graph step callbacks may call ShowSpinner/StopSpinner/Message which are
	// not goroutine-safe on the underlying console.
	origConsole := da.console
	sc := &syncConsole{Console: origConsole}

	// Create a tracker and suppress individual service spinners so
	// the tracker owns the progress display. Skip the tracker entirely in
	// machine-readable output modes (e.g. --output json) so raw progress
	// lines don't pollute stdout alongside the JSON result, and when no
	// writer is available (e.g. test mocks).
	if w := origConsole.GetWriter(); da.formatter.Kind() != output.JsonFormat && w != nil {
		serviceNames := make([]string, len(stableServices))
		for i, svc := range stableServices {
			serviceNames[i] = svc.Name
		}
		da.progressTracker = newDeployProgressTracker(
			w,
			origConsole.IsSpinnerInteractive(),
			serviceNames,
		)
		da.console = &silentSpinnerConsole{syncConsole: sc}
	} else {
		// Still wrap the console for thread-safety without suppressing spinners.
		da.console = sc
	}
	defer func() {
		da.console = origConsole
		da.progressTracker = nil
	}()

	g := exegraph.NewGraph()
	var mu sync.Mutex
	deployResults := map[string]*project.ServiceDeployResult{}

	// serviceContexts stores the packaging result so publish and deploy can consume it.
	serviceContexts := make(map[string]*project.ServiceContext, len(stableServices))
	var svcCtxMu sync.Mutex

	// Track the first Aspire deploy step so that subsequent Aspire services
	// can declare a dependency on it (build gate serialization).
	// (Handled inside addServiceStepsToGraph via buildGateKey below.)

	if _, err := addServiceStepsToGraph(g, serviceGraphOptions{
		services:        stableServices,
		serviceManager:  da.serviceManager,
		deployTimeout:   deployTimeout,
		fromPackage:     da.flags.fromPackage,
		serviceContexts: serviceContexts,
		svcCtxMu:        &svcCtxMu,
		deployResults:   deployResults,
		resultsMu:       &mu,
		onDeployTimeout: func(ctx context.Context, svc *project.ServiceConfig) {
			da.console.MessageUxItem(ctx, deployTimeoutWarning(svc.Name))
		},
		buildGateKey: aspireBuildGateKey,
	}); err != nil {
		return nil, err
	}

	// Wire progress tracker to graph step lifecycle callbacks.
	// Step names are "package-<svc>", "publish-<svc>", "deploy-<svc>".
	opts := exegraph.RunOptions{
		MaxConcurrency: da.resolveDAGConcurrency(),
		ErrorPolicy:    exegraph.FailFast,
		OnStepStart: func(stepName string) {
			if svc, ok := strings.CutPrefix(stepName, "package-"); ok {
				da.updateProgress(svc, phasePackaging, "")
			} else if svc, ok := strings.CutPrefix(stepName, "publish-"); ok {
				da.updateProgress(svc, phasePublish, "")
			} else if svc, ok := strings.CutPrefix(stepName, "deploy-"); ok {
				da.updateProgress(svc, phaseDeploying, "")
			}
		},
		OnStepDone: func(stepName string, err error) {
			if err != nil {
				// Classify terminal state: skipped (dependency failure or
				// FailFast cascade) and parent-cancellation both surface via
				// OnStepDone with a non-nil error, but they are not service
				// failures and should not render as "Failed" in the progress
				// UI.
				phase := phaseFailed
				detail := err.Error()
				switch {
				case exegraph.IsStepSkipped(err):
					phase = phaseSkipped
					detail = ""
				case errors.Is(err, context.Canceled):
					phase = phaseSkipped
					detail = "canceled"
				}
				for _, prefix := range []string{"deploy-", "publish-", "package-"} {
					if svc, ok := strings.CutPrefix(stepName, prefix); ok {
						da.updateProgress(svc, phase, detail)
						return
					}
				}
			}
			if svc, ok := strings.CutPrefix(stepName, "deploy-"); ok {
				da.updateProgress(svc, phaseDone, "")
			}
		},
	}

	projectEventArgs := project.ProjectLifecycleEventArgs{
		Project: da.projectConfig,
	}

	// Start the progress ticker if the tracker is active.
	var stopTicker func()
	if da.progressTracker != nil {
		stopTicker = da.progressTracker.StartTicker(ctx)
	}

	err = da.projectConfig.Invoke(ctx, project.ProjectEventDeploy, projectEventArgs, func() error {
		result := exegraph.RunWithResult(ctx, g, opts)
		// Log per-step timing for diagnostics and benchmarking.
		for _, st := range result.Steps {
			log.Printf("deploy-graph step %-30s  %s  %s", st.Name, st.Status, st.Duration.Round(time.Millisecond))
		}
		log.Printf("deploy-graph total: %s (%d steps)", result.TotalDuration.Round(time.Millisecond), len(result.Steps))

		// Unwrap the graph runner's step-level error prefix ("step X failed: ...")
		// so user-facing messages contain only the action error, not internal graph
		// framing. When exactly one step failed, return its inner error directly;
		// when multiple failed, join their inner errors.
		return unwrapStepErrors(result)
	})

	// Stop ticker and render final progress state.
	if stopTicker != nil {
		stopTicker()
	}
	if da.progressTracker != nil {
		da.progressTracker.RenderFinal()
	}

	// Clean up temporary package artifacts created during graph execution.
	// The graph path must do it after execution since steps run in parallel
	// and may still hold file locks.
	if da.flags.fromPackage == "" {
		for _, sc := range serviceContexts {
			for _, artifact := range sc.Package {
				if artifact.Kind == project.ArtifactKindArchive &&
					strings.HasPrefix(artifact.Location, os.TempDir()) {
					if rmErr := os.RemoveAll(artifact.Location); rmErr != nil {
						log.Printf("failed to remove temporary package: %s : %s", artifact.Location, rmErr)
					}
				}
			}
		}
	}

	// Display service endpoint artifacts collected during deploy steps.
	// The graph path must display them after execution since results are
	// collected concurrently.
	if da.formatter.Kind() != output.JsonFormat {
		for _, svc := range stableServices {
			if dr, ok := deployResults[svc.Name]; ok && dr != nil {
				da.console.MessageUxItem(ctx, dr.Artifacts)
			}
		}
	}

	if err != nil {
		return nil, err
	}

	aspireDashboardUrl := apphost.AspireDashboardUrl(ctx, da.env, da.alphaFeatureManager)
	if aspireDashboardUrl != nil {
		da.console.MessageUxItem(ctx, aspireDashboardUrl)
	}

	if da.formatter.Kind() == output.JsonFormat {
		deployResult := DeploymentResult{
			Timestamp: time.Now(),
			Services:  deployResults,
		}

		if fmtErr := da.formatter.Format(deployResult, da.writer, nil); fmtErr != nil {
			return nil, fmt.Errorf("deploy result could not be displayed: %w", fmtErr)
		}
	}

	// Invalidate cache after successful deploy so azd show will refresh
	if err := da.envManager.InvalidateEnvCache(ctx, da.env.Name()); err != nil {
		log.Printf("warning: failed to invalidate state cache: %v", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf(
				"Your application was deployed to Azure in %s.",
				ux.DurationAsText(since(startTime)),
			),
			FollowUp: getResourceGroupFollowUp(ctx,
				da.formatter,
				da.portalUrlBase,
				da.projectConfig,
				da.resourceManager,
				da.env,
				false,
			),
		},
	}, nil
}

// resolveDAGConcurrency reads AZD_DEPLOY_CONCURRENCY from the environment.
// Returns 0 (unlimited) if the variable is unset or invalid.
func (da *DeployAction) resolveDAGConcurrency() int {
	if envVal, ok := os.LookupEnv("AZD_DEPLOY_CONCURRENCY"); ok {
		if n, err := strconv.Atoi(envVal); err == nil && n > 0 {
			return min(n, 64)
		}
	}
	return 0
}

func (da *DeployAction) resolveDeployTimeout() (time.Duration, error) {
	return resolveDeployTimeout(da.flags)
}

// resolveDeployTimeout picks the deploy-per-service timeout from, in order:
//
//  1. --timeout CLI flag (if set by the user)
//  2. AZD_DEPLOY_TIMEOUT environment variable (integer seconds)
//  3. [defaultDeployTimeoutSeconds]
//
// Exposed as a free function so [UpGraphAction] — which shares the same
// [DeployFlags] type but not the [DeployAction] receiver — can resolve the
// timeout without duplicating the precedence logic.
func resolveDeployTimeout(flags *DeployFlags) (time.Duration, error) {
	if flags != nil && flags.timeoutChanged() {
		if flags.Timeout <= 0 {
			return 0, errors.New("invalid value for --timeout: must be greater than 0 seconds")
		}

		return time.Duration(flags.Timeout) * time.Second, nil
	}

	if envVal, ok := os.LookupEnv("AZD_DEPLOY_TIMEOUT"); ok {
		seconds, err := strconv.Atoi(envVal)
		if err != nil {
			return 0, fmt.Errorf("invalid AZD_DEPLOY_TIMEOUT value '%s': must be an integer number of seconds", envVal)
		}
		if seconds <= 0 {
			return 0, fmt.Errorf("invalid AZD_DEPLOY_TIMEOUT value '%d': must be greater than 0 seconds", seconds)
		}
		return time.Duration(seconds) * time.Second, nil
	}

	return time.Duration(defaultDeployTimeoutSeconds) * time.Second, nil
}

func GetCmdDeployHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription("Deploy application to Azure.", []string{
		formatHelpNote(
			"By default, deploys all services listed in 'azure.yaml' in the current directory," +
				" or the service described in the project that matches the current directory."),
		formatHelpNote(
			fmt.Sprintf("When %s is set, only the specific service is deployed.", output.WithHighLightFormat("<service>"))),
		formatHelpNote("After the deployment is complete, the endpoint is printed. To start the service, select" +
			" the endpoint or paste it in a browser."),
	})
}

func GetCmdDeployHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Deploy all services in the current project to Azure.": output.WithHighLightFormat(
			"azd deploy --all",
		),
		"Deploy the service named 'api' to Azure.": output.WithHighLightFormat(
			"azd deploy api",
		),
		"Deploy the service named 'web' to Azure.": output.WithHighLightFormat(
			"azd deploy web",
		),
		"Deploy the service named 'api' to Azure from a previously generated package.": output.WithHighLightFormat(
			"azd deploy api --from-package <package-path>",
		),
	})
}

// updateProgress notifies the progress tracker if it is active.
// When the tracker is nil (single-service path), this is a no-op.
func (da *DeployAction) updateProgress(serviceName string, phase deployPhase, detail string) {
	if da.progressTracker != nil {
		da.progressTracker.Update(serviceName, phase, detail)
	}
}

// unwrapStepErrors extracts the inner (action-level) errors from a graph
// RunResult, stripping the graph scheduler's "step %q failed: " prefix added
// by runStep. This keeps user-facing deploy errors clean — the step-name
// framing is useful for diagnostics logs but should not leak to users.
//
// Skipped steps (dependency failures) are omitted — only genuine step Action
// errors are returned. When exactly one step failed, its inner error is
// returned directly (not wrapped in errors.Join).
func unwrapStepErrors(result *exegraph.RunResult) error {
	if result.Error == nil {
		return nil
	}

	var inner []error
	for _, st := range result.Steps {
		if st.Err == nil || st.Status == exegraph.StepSkipped {
			continue
		}
		// runStep wraps with fmt.Errorf("step %q failed: %w", ...) — one Unwrap
		// level peels off that prefix while preserving the action error chain.
		if unwrapped := errors.Unwrap(st.Err); unwrapped != nil {
			inner = append(inner, unwrapped)
		} else {
			inner = append(inner, st.Err)
		}
	}

	switch len(inner) {
	case 0:
		// Shouldn't happen if result.Error != nil, but be safe.
		return result.Error
	case 1:
		return inner[0]
	default:
		return errors.Join(inner...)
	}
}

// silentSpinnerConsole wraps syncConsole but suppresses spinner output.
// When the progress table is active, the tracker owns the progress display
// and per-service spinners would interfere with the table rendering.
type silentSpinnerConsole struct {
	*syncConsole
}

func (*silentSpinnerConsole) ShowSpinner(_ context.Context, _ string, _ input.SpinnerUxType) {}
func (*silentSpinnerConsole) StopSpinner(_ context.Context, _ string, _ input.SpinnerUxType) {}
func (*silentSpinnerConsole) IsSpinnerRunning(_ context.Context) bool                        { return false }
