// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package inittelemetry records bounded, best-effort setup timings.
package inittelemetry

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Step identifies a fixed business step, never a customer-supplied value.
type Step string

const (
	AgentNameInput                 Step = "agent_name_input"
	AgentDescriptionInput          Step = "agent_description_input"
	AgentInstructionsInput         Step = "agent_instructions_input"
	AgentNameConflictConfirmation  Step = "agent_name_conflict_confirmation"
	AgentNameConflictResolution    Step = "agent_name_conflict_resolution"
	ProjectReuseConfirmation       Step = "project_reuse_confirmation"
	InitModeSelection              Step = "init_mode_selection"
	TemplateLanguageSelection      Step = "template_language_selection"
	TemplateSelection              Step = "template_selection"
	ProjectModeSelection           Step = "project_mode_selection"
	ProjectReplacementConfirmation Step = "project_replacement_confirmation"
	FoundryProjectSelection        Step = "foundry_project_selection"
	FoundryProjectNameInput        Step = "foundry_project_name_input"
	SubscriptionSelection          Step = "subscription_selection"
	LocationSelection              Step = "location_selection"
	ModelStrategySelection         Step = "model_strategy_selection"
	ModelSelection                 Step = "model_selection"
	ModelDeploymentSelection       Step = "model_deployment_selection"
	ModelDeploymentNameInput       Step = "model_deployment_name_input"
	ModelLocationSelection         Step = "model_location_selection"
	ModelLocationMismatchSelection Step = "model_location_mismatch_selection"
	ModelAlternativeSelection      Step = "model_alternative_selection"
	DeployModeSelection            Step = "deploy_mode_selection"
	RuntimeSelection               Step = "runtime_selection"
	EntryPointInput                Step = "entry_point_input"
	DependencyResolutionSelection  Step = "dependency_resolution_selection"
	ProtocolSelection              Step = "protocol_selection"
	ServiceSelection               Step = "service_selection"
	StartupCommandInput            Step = "startup_command_input"
	ContainerResourcesSelection    Step = "container_resources_selection"
	ResourceCollisionSelection     Step = "resource_collision_selection"
	ResourceNameInput              Step = "resource_name_input"
	EnvironmentValueInput          Step = "environment_value_input"
	AcrConnectionInput             Step = "acr_connection_input"
	AcrConnectionSelection         Step = "acr_connection_selection"
	AppInsightsResourceInput       Step = "app_insights_resource_input"
	AppInsightsConnectionInput     Step = "app_insights_connection_input"
	AppInsightsConnectionSelection Step = "app_insights_connection_selection"
	RaiPolicySelection             Step = "rai_policy_selection"
	InputValidation                Step = "input_validation"
	ManifestValidation             Step = "manifest_validation"
	ManifestLoading                Step = "manifest_loading"
	TemplateCatalogLoading         Step = "template_catalog_loading"
	TemplateDownload               Step = "template_download"
	Authentication                 Step = "authentication"
	AiServiceCapabilityCheck       Step = "ai_service_capability_check"
	ModelCatalogLoading            Step = "model_catalog_loading"
	ModelDeploymentResolution      Step = "model_deployment_resolution"
	FoundryProjectDiscovery        Step = "foundry_project_discovery"
	FoundryProjectLookup           Step = "foundry_project_lookup"
	ProjectConnectionDiscovery     Step = "project_connection_discovery"
	ModelDeploymentDiscovery       Step = "model_deployment_discovery"
	TenantLookup                   Step = "tenant_lookup"
	RaiPolicyDiscovery             Step = "rai_policy_discovery"
	RegistryConnectionValidation   Step = "registry_connection_validation"
	ProjectScaffolding             Step = "project_scaffolding"
	ProjectAuthoring               Step = "project_authoring"
	ModelDeploymentAuthoring       Step = "model_deployment_authoring"
	ProjectConfigurationUpdate     Step = "project_configuration_update"
	ProjectConfigurationLoading    Step = "project_configuration_loading"
	EnvironmentConfiguration       Step = "environment_configuration"
	FileGeneration                 Step = "file_generation"
	InfraGeneration                Step = "infra_generation"
	PostInitValidation             Step = "post_init_validation"
)

// Category distinguishes local work, external operations, and simple prompt RPCs.
type Category string

const (
	Prompt    Category = "prompt"
	Execution Category = "execution"
	External  Category = "external"
)

// Source identifies a resolved input's origin, not its value.
type Source string

const (
	User        Source = "user"
	Flag        Source = "flag"
	Config      Source = "config"
	Environment Source = "environment"
	Default     Source = "default"
)

// Flow identifies the extension command that owns an invocation's sequence and summary.
type Flow string

const (
	AgentInit            Flow = "agent.init"
	ProjectAdd           Flow = "project.add"
	ProjectDeploymentAdd Flow = "project.deployment.add"
)

const (
	maxBufferedSteps = 96
	reportTimeout    = time.Second
	flowVersion      = "1"
)

type contextKey struct{}

type stepRecord struct {
	id       Step
	category Category
	source   Source
	scope    string
	sequence int
	duration time.Duration
	outcome  string
}

// Tracker keeps one command's timings without performing I/O during its steps.
type Tracker struct {
	mu             sync.Mutex
	now            func() time.Time
	flow           Flow
	noPrompt       bool
	sequence       int
	steps          []stepRecord
	promptCount    int
	promptDuration time.Duration
	longest        stepRecord
	dropped        int
}

// NewContext attaches an invocation-local tracker; nested actions reuse it.
func NewContext(ctx context.Context, flow Flow, noPrompt bool) (context.Context, *Tracker) {
	if tracker, ok := ctx.Value(contextKey{}).(*Tracker); ok {
		return ctx, tracker
	}
	tracker := &Tracker{now: time.Now, flow: flow, noPrompt: noPrompt}
	return context.WithValue(ctx, contextKey{}, tracker), tracker
}

// Start begins a step only when the command has enabled setup telemetry.
// Call the returned function with the operation's error, including on early returns.
func Start(ctx context.Context, id Step, category Category, source Source) func(error) {
	return start(ctx, id, category, source, "operation")
}

func start(ctx context.Context, id Step, category Category, source Source, scope string) func(error) {
	tracker, ok := ctx.Value(contextKey{}).(*Tracker)
	if !ok {
		return func(error) {}
	}
	tracker.mu.Lock()
	tracker.sequence++
	sequence := tracker.sequence
	started := tracker.now()
	tracker.mu.Unlock()
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			tracker.mu.Lock()
			defer tracker.mu.Unlock()
			record := stepRecord{
				id: id, category: category, source: source, scope: scope, sequence: sequence,
				duration: tracker.now().Sub(started), outcome: outcome(err),
			}
			if category == Prompt {
				tracker.promptCount++
				tracker.promptDuration += record.duration
				if tracker.longest.id == "" || record.duration > tracker.longest.duration {
					tracker.longest = record
				}
			}
			if len(tracker.steps) < maxBufferedSteps {
				tracker.steps = append(tracker.steps, record)
			} else {
				tracker.dropped++
			}
		})
	}
}

// Measure records a leaf operation without changing its result or error.
func Measure[T any](ctx context.Context, id Step, category Category, source Source, call func() (T, error)) (T, error) {
	end := Start(ctx, id, category, source)
	value, err := call()
	end(err)
	return value, err
}

// MeasureError is Measure for operations that return only an error.
func MeasureError(ctx context.Context, id Step, category Category, source Source, call func() error) error {
	end := Start(ctx, id, category, source)
	err := call()
	end(err)
	return err
}

func outcome(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return "timeout"
	}
	if timeoutErr, ok := errors.AsType[net.Error](err); ok && timeoutErr.Timeout() {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return "canceled"
	}
	if localErr, ok := errors.AsType[*azdext.LocalError](err); ok &&
		localErr.Category == azdext.LocalErrorCategoryUser && localErr.Code == "cancelled" {
		return "canceled"
	}
	return "error"
}

// Report connects only to the existing azd telemetry service, never to Azure.
func (t *Tracker) Report(ctx context.Context, err error) {
	client, clientErr := azdext.NewAzdClient()
	if clientErr != nil {
		azdext.NewLogger("init.telemetry").Debug("could not create setup telemetry client")
		return
	}
	defer client.Close()
	t.Flush(ctx, foundryTelemetry.NewReporter(client.Telemetry(), nil), err)
}

// Flush reports the summary first, then completed steps in start order.
// One shared deadline bounds reporting overhead, even when Init was canceled.
func (t *Tracker) Flush(ctx context.Context, reporter foundryTelemetry.Reporter, err error) {
	if reporter == nil {
		return
	}
	t.mu.Lock()
	steps := append([]stepRecord(nil), t.steps...)
	promptCount, promptDuration, longest := t.promptCount, t.promptDuration, t.longest
	sequence, dropped := t.sequence, t.dropped
	t.mu.Unlock()
	if dropped > 0 {
		azdext.NewLogger("init.telemetry").Debug("setup timing buffer limit reached", "dropped", dropped)
	}
	longestID := "none"
	if longest.id != "" {
		longestID = string(longest.id)
	}
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportTimeout)
	defer cancel()
	reporter.Report(reportCtx, foundryTelemetry.Event{
		Name: string(t.flow) + ".summary",
		Attributes: map[string]string{
			"init.flow.version":               flowVersion,
			"init.flow.scope":                 string(t.flow),
			"init.flow.outcome":               outcome(err),
			"init.prompt.count":               strconv.Itoa(promptCount),
			"init.prompt.total_duration_ms":   milliseconds(promptDuration),
			"init.prompt.longest.id":          longestID,
			"init.prompt.longest_duration_ms": milliseconds(longest.duration),
			"init.step.count":                 strconv.Itoa(sequence),
			"init.step.dropped_count":         strconv.Itoa(dropped),
		},
	})
	slices.SortFunc(steps, func(a, b stepRecord) int { return a.sequence - b.sequence })
	for _, step := range steps {
		if reportCtx.Err() != nil {
			azdext.NewLogger("init.telemetry").Debug("setup telemetry reporting deadline reached")
			break
		}
		if step.source != "" {
			reporter.Report(reportCtx, foundryTelemetry.Event{
				Name: string(t.flow) + ".step",
				Attributes: map[string]string{
					"init.flow.version":      flowVersion,
					"init.flow.scope":        string(t.flow),
					"init.step.id":           string(step.id),
					"init.step.category":     string(step.category),
					"init.step.sequence":     strconv.Itoa(step.sequence),
					"init.step.outcome":      step.outcome,
					"init.step.timing_scope": step.scope,
					"init.step.input_source": string(step.source),
					"duration_ms":            milliseconds(step.duration),
				},
			})
		} else {
			reporter.Report(reportCtx, foundryTelemetry.Event{
				Name: string(t.flow) + ".step",
				Attributes: map[string]string{
					"init.flow.version":      flowVersion,
					"init.flow.scope":        string(t.flow),
					"init.step.id":           string(step.id),
					"init.step.category":     string(step.category),
					"init.step.sequence":     strconv.Itoa(step.sequence),
					"init.step.outcome":      step.outcome,
					"init.step.timing_scope": step.scope,
					"duration_ms":            milliseconds(step.duration),
				},
			})
		}
	}
}

func milliseconds(duration time.Duration) string {
	return strconv.FormatFloat(float64(duration)/float64(time.Millisecond), 'f', 3, 64)
}
