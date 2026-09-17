// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	agentTelemetry "azureaiagent/internal/telemetry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"go.yaml.in/yaml/v3"
)

type initOperationContextKey struct{}

// initOperationContext stores bounded classifications only. It never owns or
// changes command results; refinements replace intent instead of double-counting it.
type initOperationContext struct {
	classes []agentTelemetry.OperationClass
}

func withInitOperationContext(ctx context.Context, kind string, explicitInput bool) context.Context {
	state := &initOperationContext{}
	if !explicitInput && kind != "" {
		state.classes = []agentTelemetry.OperationClass{
			agentTelemetry.ClassifyOperation(map[string]any{"kind": kind}),
		}
	}
	return context.WithValue(ctx, initOperationContextKey{}, state)
}

func recordInitProperties(ctx context.Context, properties map[string]any) {
	if state, ok := ctx.Value(initOperationContextKey{}).(*initOperationContext); ok {
		state.classes = []agentTelemetry.OperationClass{agentTelemetry.ClassifyOperation(properties)}
	}
}

func recordInitDefinition(ctx context.Context, definition any) {
	if ctx.Value(initOperationContextKey{}) == nil {
		return
	}
	data, err := json.Marshal(definition)
	if err != nil {
		return
	}
	var properties map[string]any
	if json.Unmarshal(data, &properties) == nil {
		recordInitProperties(ctx, properties)
	}
}

func recordInitProjectContent(ctx context.Context, content []byte) {
	state, ok := ctx.Value(initOperationContextKey{}).(*initOperationContext)
	if !ok {
		return
	}
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if yaml.Unmarshal(content, &doc) != nil {
		return
	}
	var classes []agentTelemetry.OperationClass
	for _, properties := range doc.Services {
		if properties["host"] == AiAgentHost {
			classes = append(classes, agentTelemetry.ClassifyOperation(properties))
		}
	}
	if len(classes) > 0 {
		state.classes = classes
	}
}

func recordInitProject(ctx context.Context, project *azdext.ProjectConfig) {
	if state, ok := ctx.Value(initOperationContextKey{}).(*initOperationContext); ok {
		state.classes = operationProjectClasses(project)
	}
}

func reportInitOperation(ctx context.Context) {
	state, ok := ctx.Value(initOperationContextKey{}).(*initOperationContext)
	if !ok {
		return
	}
	// A cancelled command must remain cancelled. A separate, bounded reporting
	// context preserves its trace metadata but never retries the operation.
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	client, err := azdext.NewAzdClient()
	if err != nil {
		return
	}
	defer client.Close()
	reporter := foundryTelemetry.NewReporter(client.Telemetry(), nil)
	newOperationReporter().report(reportCtx, reporter, "init", state.classes)
}

type operationReporter struct {
	mu   sync.Mutex
	seen map[string]bool
}

func newOperationReporter() *operationReporter {
	return &operationReporter{seen: map[string]bool{}}
}

func (r *operationReporter) report(
	ctx context.Context, reporter foundryTelemetry.Reporter, operation string, classes []agentTelemetry.OperationClass,
) {
	if len(classes) == 0 {
		classes = []agentTelemetry.OperationClass{{Category: "unknown", Telephony: "unknown"}}
	}
	// Deterministic order and one shared budget bound the total telemetry delay,
	// not one second per service. Keep the original context reporter untouched.
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	names := map[string]foundryTelemetry.Event{}
	for _, class := range classes {
		if event, ok := agentTelemetry.OperationClassified(operation, class); ok {
			names[event.Name] = event
		}
	}
	keys := slices.Sorted(maps.Keys(names))
	for _, name := range keys {
		r.mu.Lock()
		seen := r.seen[name]
		r.seen[name] = true
		r.mu.Unlock()
		if !seen && ctx.Err() == nil && reporter != nil {
			reporter.Report(ctx, names[name])
		}
	}
}

func operationServiceClass(svc *azdext.ServiceConfig) agentTelemetry.OperationClass {
	unknown := agentTelemetry.OperationClass{Category: "unknown", Telephony: "unknown"}
	if svc == nil || strings.TrimSpace(os.Getenv("AGENT_DEFINITION_PATH")) != "" {
		return unknown // do not read external definitions just to collect telemetry
	}
	properties := svc.GetAdditionalProperties()
	if len(properties.GetFields()) == 0 {
		properties = svc.GetConfig()
	}
	return agentTelemetry.ClassifyOperation(properties.AsMap())
}

func operationProjectClasses(project *azdext.ProjectConfig) []agentTelemetry.OperationClass {
	var classes []agentTelemetry.OperationClass
	for _, svc := range project.GetServices() {
		if svc.GetHost() == AiAgentHost {
			classes = append(classes, operationServiceClass(svc))
		}
	}
	return classes
}
