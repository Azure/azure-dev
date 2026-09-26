// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"slices"
	"strings"

	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
)

// OperationEventPrefix uses the existing extension.event field, not new attributes.
// Names contain only allowlisted product classifications, never customer strings.
const OperationEventPrefix = "agent.operation.v1."

// OperationClass describes an in-memory configuration, not an operation outcome.
type OperationClass struct {
	Category  string
	Telephony string
}

// ClassifyOperation reads only supplied properties. It never resolves references,
// accesses files, expands environment variables, validates, or mutates a service.
func ClassifyOperation(properties map[string]any) OperationClass {
	result := OperationClass{Category: "unknown", Telephony: "unknown"}
	if _, unresolved := properties["$ref"]; unresolved {
		return result
	}
	text := func(key string) string {
		value, _ := properties[key].(string)
		return strings.ToLower(strings.TrimSpace(value))
	}
	switch text("kind") {
	case "hosted":
		result.Category = "hosted"
		protocols, _ := properties["protocols"].([]any)
		for _, value := range protocols {
			protocol, _ := value.(map[string]any)
			name, _ := protocol["protocol"].(string)
			if name == "invocations_ws" {
				result.Category = "hosted_invocations_ws"
			}
		}
	case "prompt":
		result.Category = "prompt"
	case "workflow":
		result.Category = "workflow"
	case "voice", "prompt-voice":
		engine, hasEngine := properties["conversationEngine"]
		if !hasEngine {
			engine, hasEngine = properties["conversation_engine"]
		}
		if hasEngine {
			fields, _ := engine.(map[string]any)
			engineType, _ := fields["type"].(string)
			if strings.EqualFold(strings.TrimSpace(engineType), "hosted_agent") {
				result.Category = "voice_hosted_wrapper"
			}
		} else {
			value, present := properties["modelType"]
			if !present {
				value, present = properties["model_type"]
			}
			mode, valid := value.(string)
			if present && !valid {
				return result
			}
			mode = strings.ToLower(strings.TrimSpace(mode))
			switch mode {
			case "", "managed":
				result.Category = "voice_managed"
			case "self_deployed":
				result.Category = "voice_byom"
			}
		}
	}
	if result.Category == "unknown" {
		return result
	}
	result.Telephony = "none"
	if value, found := properties["telephony"]; found {
		fields, ok := value.(map[string]any)
		if !ok {
			result.Telephony = "unknown"
		} else if value, present := fields["bindings"]; present {
			bindings, ok := value.([]any)
			if !ok {
				result.Telephony = "unknown"
			} else if len(bindings) > 0 {
				result.Telephony = "enabled"
			}
		}
	}
	return result
}

// OperationClassified returns a bounded event, or false for an unrelated operation.
// Outcomes remain exclusively owned by the existing command completion telemetry.
func OperationClassified(operation string, class OperationClass) (foundryTelemetry.Event, bool) {
	if !slices.Contains([]string{"init", "provision", "deploy"}, operation) {
		return foundryTelemetry.Event{}, false
	}
	if !slices.Contains([]string{
		"hosted", "hosted_invocations_ws", "prompt", "workflow",
		"voice_managed", "voice_byom", "voice_hosted_wrapper", "unknown",
	}, class.Category) {
		class.Category = "unknown"
	}
	if !slices.Contains([]string{"none", "enabled", "unknown"}, class.Telephony) {
		class.Telephony = "unknown"
	}
	return foundryTelemetry.Event{
		Name: OperationEventPrefix + operation + "." + class.Category + "." + class.Telephony,
	}, true
}
