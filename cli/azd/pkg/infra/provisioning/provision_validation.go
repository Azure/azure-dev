// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// provisionValidationTimeout bounds how long the provider-agnostic provision
// validation waits for extension checks before skipping them. It mirrors the
// bound the Bicep provider applies to its "local-preflight" dispatch so a
// blocked or unresponsive extension check cannot hang provisioning.
const provisionValidationTimeout = 60 * time.Second

// Provider-agnostic provision validation outcomes recorded in telemetry. These
// mirror the outcome values emitted by the Bicep "local-preflight" dispatch so
// that both validation sites report a consistent vocabulary.
const (
	provisionValidationOutcomePassed           = "passed"
	provisionValidationOutcomeWarningsAccepted = "warnings_accepted"
	provisionValidationOutcomeAbortedByErrors  = "aborted_by_errors"
	provisionValidationOutcomeAbortedByUser    = "aborted_by_user"
	provisionValidationOutcomeError            = "error"
)

// ErrProvisionValidationAborted signals that the provider-agnostic provision
// validation aborted (an error-severity finding, or the user declining to
// continue past warnings). The provision/up command layer passes it through
// wrapProvisionError, which emits the "Provisioning was cancelled." message and
// translates it into the standard [internal.ErrAbortedByUser] outcome.
var ErrProvisionValidationAborted = errors.New("provisioning aborted during validation")

// RunProvisionValidation dispatches the provider-agnostic "provision"
// validation checks registered by extensions. It is invoked once per
// `azd provision` / `azd up`, before the layer graph runs, rather than inside
// [Manager.Deploy]/[Manager.Preview] — in multi-layer provisioning each layer
// has its own Manager whose Deploy runs concurrently, so dispatching here keeps
// the checks (and any warning confirmation prompt) firing exactly once with the
// single, env-scoped context. Unlike the Bicep-only "local-preflight" dispatch,
// this runs for every provider (Bicep, Terraform, and extension-provided
// providers such as microsoft.foundry and demo).
//
// Findings render through the uniform preflight report. The return value is a
// single error the command layer passes to wrapProvisionError:
//   - [ErrProvisionValidationAborted]: an error-severity finding, or the user
//     declining to continue past warnings. wrapProvisionError translates this
//     into the standard [internal.ErrAbortedByUser] outcome (exit code 0).
//   - any other error: the confirmation prompt itself failed to run.
//   - nil: validation passed, was skipped, or its warnings were accepted.
//
// Dispatch/timeout failures are non-fatal: they are logged and treated as a
// skip (nil), so a blocked or unreachable extension never blocks provisioning.
//
// The preview flag only affects the confirmation prompt wording.
func (m *Manager) RunProvisionValidation(ctx context.Context, preview bool) (err error) {
	// Respect the same gate as the Bicep preflight so users can disable all
	// client-side validation with a single setting.
	if m.provisionValidationDisabled() {
		return nil
	}

	// The dispatcher is optional — when no extensions are loaded it is not
	// registered, so there is nothing to validate.
	var dispatcher ValidationCheckDispatcher
	if resolveErr := m.serviceLocator.Resolve(&dispatcher); resolveErr != nil {
		return nil
	}

	ctx, span := tracing.Start(ctx, events.PreflightValidationEvent)
	defer func() {
		span.EndWithStatus(err)
	}()

	// Tag the dispatch site so this event can be distinguished from the Bicep
	// provider's "local-preflight" emission (both share PreflightValidationEvent
	// and, for Bicep provisions, both fire in a single run).
	span.SetAttributes(fields.PreflightCheckTypeKey.String(azdext.ValidationCheckTypeProvision))

	checkContext := m.provisionValidationContext()

	// Bound extension dispatch so a blocked check cannot hang provisioning. A
	// timeout (or any dispatch error) is treated as a non-fatal skip.
	dispatchCtx, cancel := context.WithTimeout(ctx, provisionValidationTimeout)
	results, invokedRuleIDs, dispatchErr := dispatcher.DispatchChecks(
		dispatchCtx, azdext.ValidationCheckTypeProvision, checkContext,
	)
	cancel()
	if dispatchErr != nil {
		if errors.Is(dispatchErr, context.DeadlineExceeded) {
			log.Printf(
				"provision validation checks timed out after %s; skipping: %v",
				provisionValidationTimeout, dispatchErr,
			)
		} else {
			log.Printf("provision validation checks failed: %v", dispatchErr)
		}
	}

	if len(invokedRuleIDs) > 0 {
		span.SetAttributes(fields.PreflightExtensionRulesKey.StringSlice(invokedRuleIDs))
	}

	if len(results) == 0 {
		// Distinguish a genuine pass (checks ran and found nothing) from a
		// dispatch failure/timeout where the checks did not actually run.
		// Provisioning proceeds either way (dispatch errors are non-fatal), but
		// recording "passed" for a failed dispatch would mask the failure in
		// telemetry, so report the error outcome instead.
		outcome := provisionValidationOutcomePassed
		if dispatchErr != nil {
			outcome = provisionValidationOutcomeError
		}
		span.SetAttributes(fields.PreflightOutcomeKey.String(outcome))
		return nil
	}

	report := &ux.PreflightReport{}
	var diagnosticIDs []string
	var warningCount, errorCount int
	for _, result := range results {
		isError := result.Severity ==
			azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_ERROR
		if isError {
			errorCount++
		} else {
			warningCount++
		}
		if result.DiagnosticId != "" {
			diagnosticIDs = append(diagnosticIDs, result.DiagnosticId)
		}
		links := make([]ux.PreflightReportLink, len(result.Links))
		for i, l := range result.Links {
			links[i] = ux.PreflightReportLink{Title: l.Text, URL: l.Url}
		}
		report.Items = append(report.Items, ux.PreflightReportItem{
			IsError:      isError,
			DiagnosticID: result.DiagnosticId,
			Message:      result.Message,
			Suggestion:   result.Suggestion,
			Links:        links,
		})
	}
	span.SetAttributes(fields.PreflightDiagnosticsKey.StringSlice(diagnosticIDs))
	span.SetAttributes(fields.PreflightWarningCountKey.Int(warningCount))
	span.SetAttributes(fields.PreflightErrorCountKey.Int(errorCount))

	m.console.MessageUxItem(ctx, report)

	if report.HasErrors() {
		// Errors were already displayed by the report above. Validation
		// successfully detected problems and provisioning is intentionally
		// aborted — this is not an internal failure (exit code 0).
		m.console.Message(ctx, "Provision validation detected errors, provisioning aborted.")
		span.SetAttributes(fields.PreflightOutcomeKey.String(provisionValidationOutcomeAbortedByErrors))
		return ErrProvisionValidationAborted
	}

	if report.HasWarnings() {
		m.console.Message(ctx, "")
		action := "provisioning"
		if preview {
			action = "the preview"
		}
		continueProvision, promptErr := m.console.Confirm(ctx, input.ConsoleOptions{
			Message:      fmt.Sprintf("Proceed with %s despite the warnings above?", action),
			DefaultValue: true,
		})
		if promptErr != nil {
			span.SetAttributes(fields.PreflightOutcomeKey.String(provisionValidationOutcomeError))
			return fmt.Errorf("prompting for provision validation confirmation: %w", promptErr)
		}
		if !continueProvision {
			span.SetAttributes(fields.PreflightOutcomeKey.String(provisionValidationOutcomeAbortedByUser))
			return ErrProvisionValidationAborted
		}
		span.SetAttributes(fields.PreflightOutcomeKey.String(provisionValidationOutcomeWarningsAccepted))
	}

	return nil
}

// provisionValidationDisabled reports whether client-side provision validation
// is turned off via the `provision.preflight` user config (value "off"). It
// shares the gate with the Bicep preflight so a single setting disables both.
func (m *Manager) provisionValidationDisabled() bool {
	var userConfigManager config.UserConfigManager
	if err := m.serviceLocator.Resolve(&userConfigManager); err != nil {
		return false
	}
	userConfig, err := userConfigManager.Load()
	if err != nil {
		return false
	}
	val, exists := userConfig.GetString("provision.preflight")
	return exists && val == "off"
}

// provisionValidationContext builds the lean, provider-agnostic context passed
// to "provision" validation checks. It intentionally omits any ARM template or
// parameters, since not every provider (e.g. Terraform, foundry) produces one.
func (m *Manager) provisionValidationContext() map[string][]byte {
	resourceGroup := m.env.Getenv(environment.ResourceGroupEnvVarName)
	targetScope := "subscription"
	if resourceGroup != "" {
		targetScope = "resourceGroup"
	}

	return map[string][]byte{
		azdext.ValidationContextEnvName:        []byte(m.env.Name()),
		azdext.ValidationContextSubscriptionID: []byte(m.env.GetSubscriptionId()),
		azdext.ValidationContextEnvLocation:    []byte(strings.ToLower(m.env.GetLocation())),
		azdext.ValidationContextResourceGroup:  []byte(resourceGroup),
		azdext.ValidationContextTargetScope:    []byte(targetScope),
	}
}
