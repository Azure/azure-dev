// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The scanner discovers telemetry attribute keys by recognizing payload literals
// (foundry telemetry.Event and azdext ReportUsageRequest) wherever they appear in
// extension source and reading their inline Attributes map. Keys that cannot be
// read statically are rejected. To keep every key discoverable, the scanner also
// enforces three strict rules on extension code:
//
//   - Attributes and GetAttributes may not be read or assigned through a selector
//     outside a payload literal (rule 1).
//   - A type-elided composite literal may not carry an Attributes entry (rule 2).
//   - A telemetry payload type may not be aliased; the alias is rejected at its
//     declaration (rule 3).
//
// These tests pin the supported grammar and each rejection.

// --- Extraction of declared keys from payload literals ---

func TestScanExtractsReportUsageLiteralKeys(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.demo/internal/cmd/telemetry.go", `package cmd

import v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"

var _ = &v1beta.ReportUsageRequest{
	EventName: "demo.telemetry.reported",
	Attributes: map[string]string{
		"demo.mode":    "sample",
		"demo.outcome": "completed",
	},
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, diagnostics)
	require.Equal(t, []string{"demo.mode", "demo.outcome"}, usageKeys(usages))
	require.Equal(t, "contoso.demo", usages[0].extension)
}

func TestScanFoldsSamePackageConstantKeys(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/telemetry/events.go", `package telemetry

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

const routeAttribute = "route"

func LocalClientRouteSelected(route string) foundryTelemetry.Event {
	return foundryTelemetry.Event{
		Name:       "local_client.route.selected",
		Attributes: map[string]string{routeAttribute: route},
	}
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, diagnostics)
	require.Equal(t, []string{"route"}, usageKeys(usages))
}

// The payload-literal approach discovers keys regardless of the file name: a
// payload built in run.go is scanned exactly like one in telemetry.go.
func TestScanFindsPayloadsInAnyFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/run.go", `package cmd

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

var _ = foundryTelemetry.Event{Attributes: map[string]string{"route": "inspector"}}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, diagnostics)
	require.Equal(t, []string{"route"}, usageKeys(usages))
}

func TestScanExtractsPointerPayload(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

var _ = &azdext.ReportUsageRequest{Attributes: map[string]string{"agent.kind": "hosted"}}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, diagnostics)
	require.Equal(t, []string{"agent.kind"}, usageKeys(usages))
}

// --- The Attributes map must be a static inline literal ---

func TestScanRejectsNonStaticAttributeKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.demo/telemetry.go", `package demo

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

func report(dynamic string) *azdext.ReportUsageRequest {
	return &azdext.ReportUsageRequest{Attributes: map[string]string{dynamic: "value"}}
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "must be a string literal or same-package constant")
}

func TestScanRejectsNonInlineAttributes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.demo/telemetry.go", `package demo

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

func report(attributes map[string]string) *azdext.ReportUsageRequest {
	return &azdext.ReportUsageRequest{Attributes: attributes}
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "must be an inline map literal")
}

func TestScanRejectsUnkeyedPayload(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/telemetry/events.go", `package telemetry

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

var _ = foundryTelemetry.Event{"reported", map[string]string{"route": "inspector"}}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "keyed fields")
}

// --- Payloads must be built as a single concrete keyed literal ---

func TestScanRejectsPayloadContainer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/telemetry/events.go", `package telemetry

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

var _ = []foundryTelemetry.Event{{Attributes: map[string]string{"route": "inspector"}}}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "single keyed literal")
}

// A named wrapper type whose underlying type is a payload slice elides its
// element type, hiding keys, so the scanner resolves the wrapper and rejects it.
func TestScanRejectsNamedPayloadContainer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/telemetry/events.go", `package telemetry

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

type Events []foundryTelemetry.Event

func build() Events {
	return Events{{
		Name:       "example.reported",
		Attributes: map[string]string{"undeclared": "value"},
	}}
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "single keyed literal")
}

// Rule 2: a type-elided composite literal that carries an Attributes entry hides
// which concrete type is built, so it is rejected regardless of its outer type.
func TestScanRejectsTypeElidedAttributesLiteral(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/telemetry/events.go", `package telemetry

type inspectorModel struct{ Attributes map[string]string }

var _ = []inspectorModel{{Attributes: map[string]string{"undeclared": "value"}}}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "not a type-elided literal")
}

// --- Rule 1: Attributes/GetAttributes may not be accessed through a selector ---

// Writing to Attributes after construction hides the key from the inline scan.
func TestScanRejectsPostConstructionMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/telemetry/events.go", `package telemetry

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

func build() foundryTelemetry.Event {
	event := foundryTelemetry.Event{Attributes: map[string]string{}}
	event.Attributes["route"] = "inspector"
	return event
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "hides keys from governance")
}

// A getter that returns the Attributes map is rejected like a direct field read.
func TestScanRejectsGetAttributesAccess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"

func report(req *v1beta.ReportUsageRequest, dynamicKey string) {
	req.GetAttributes()[dynamicKey] = "value"
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], ".GetAttributes")
}

// Reading Attributes into a local aliases the map so later writes escape the
// inline scan; the aliasing read itself is therefore rejected.
func TestScanRejectsAttributesMapAliasing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

func decorate(req *azdext.ReportUsageRequest, dynamicKey string) {
	attrs := req.Attributes
	attrs[dynamicKey] = "value"
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "hides keys from governance")
}

// The strict rule rejects any Attributes selector, even on an unrelated struct.
// This is the accepted tradeoff: such a field must be renamed or exempted.
func TestScanRejectsUnrelatedAttributesAccess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

type inspectorModel struct{ Attributes map[string]string }

func decorate(model inspectorModel, key string) {
	model.Attributes[key] = "value"
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "rename unrelated fields")
}

// --- Rule 3: telemetry payload types may not be aliased ---

func TestScanRejectsLocalPayloadTypeAlias(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

type Usage = azdext.ReportUsageRequest

var _ = Usage{Attributes: map[string]string{"undeclared": "value"}}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "do not alias or redefine telemetry payload types")
}

// A defined type (not an alias) whose underlying type is a telemetry payload can
// be converted back to the payload, so its declaration is rejected and the
// undeclared key it would smuggle through the conversion is never accepted.
func TestScanRejectsDefinedPayloadType(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

type Usage azdext.ReportUsageRequest

var _ = azdext.ReportUsageRequest(Usage{Attributes: map[string]string{"undeclared": "value"}})
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 2)
	joined := strings.Join(diagnostics, "\n")
	require.Contains(t, joined, "do not alias or redefine telemetry payload types")
	require.Contains(t, joined, "type Usage)")
	require.Contains(t, joined, "not conversions from another type")
}

// A chain of defined types reaches the payload through its base type, so every
// declaration in the chain is rejected just like a chain of aliases.
func TestScanRejectsChainedDefinedPayloadType(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

type Usage azdext.ReportUsageRequest
type Report Usage

var _ = azdext.ReportUsageRequest(Report{Attributes: map[string]string{"undeclared": "value"}})
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 3)
	joined := strings.Join(diagnostics, "\n")
	require.Contains(t, joined, "type Usage)")
	require.Contains(t, joined, "type Report)")
	require.Contains(t, joined, "not conversions from another type")
}

// A structurally identical local type can be converted directly to a telemetry
// payload without declaring a payload alias. Reject the conversion itself so a
// dynamic Attributes map cannot bypass key discovery.
func TestScanRejectsPayloadConversionFromLocalStruct(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

type localEvent struct {
	Name       string
	Attributes map[string]string
}

func build(name string, attributes map[string]string) foundryTelemetry.Event {
	return foundryTelemetry.Event(localEvent{
		Name:       name,
		Attributes: attributes,
	})
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "not conversions from another type")
}

// A chain of aliases resolves to a payload, so each alias declaration is rejected.
func TestScanRejectsChainedPayloadAlias(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

type Usage = azdext.ReportUsageRequest
type Report = Usage

var _ = Report{Attributes: map[string]string{"undeclared": "value"}}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 2)
	joined := strings.Join(diagnostics, "\n")
	require.Contains(t, joined, "type Usage)")
	require.Contains(t, joined, "type Report)")
}

// A local alias to a payload alias re-exported from another package resolves
// across the module, so both alias declarations are rejected.
func TestScanRejectsLocalAliasToCrossPackagePayloadAlias(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/go.mod", "module github.com/contoso/agent\n\ngo 1.24\n")
	writeExtensionSource(t, root, "contoso.agent/internal/shared/telemetry.go", `package shared

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

type Usage = azdext.ReportUsageRequest
`)
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/report.go", `package cmd

import "github.com/contoso/agent/internal/shared"

type Report = shared.Usage

var _ = Report{Attributes: map[string]string{"undeclared": "value"}}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 2)
	joined := strings.Join(diagnostics, "\n")
	require.Contains(t, joined, "type Usage)")
	require.Contains(t, joined, "type Report)")
}

// --- Telemetry sink calls must pass an inline payload literal ---

// The canonical emission passes an inline payload literal to ReportUsage, so its
// keys are scanned and no diagnostic is produced.
func TestScanAcceptsInlineSinkPayload(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func emit(ctx context.Context, telemetry azdext.TelemetryServiceClient) {
	_, _ = telemetry.ReportUsage(ctx, &azdext.ReportUsageRequest{
		EventName:  "agent.context.resolved",
		Attributes: map[string]string{"agent.kind": "hosted"},
	})
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, diagnostics)
	require.Equal(t, []string{"agent.kind"}, usageKeys(usages))
}

// A payload reaching ReportUsage as a parameter carries no scanned literal, so the
// host would emit keys that governance never saw; the sink call is rejected.
func TestScanRejectsParameterSinkPayload(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func emit(ctx context.Context, telemetry azdext.TelemetryServiceClient, req *azdext.ReportUsageRequest) {
	_, _ = telemetry.ReportUsage(ctx, req)
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "inline keyed")
}

// A payload decoded into an empty request smuggles dynamic keys the scanner cannot
// see, so the ReportUsage call that emits it is rejected even though the empty
// literal itself declares nothing.
func TestScanRejectsDecodedSinkPayload(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

import (
	"context"
	"encoding/json"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func emit(ctx context.Context, telemetry azdext.TelemetryServiceClient, data []byte) {
	req := &azdext.ReportUsageRequest{}
	_ = json.Unmarshal(data, req)
	_, _ = telemetry.ReportUsage(ctx, req)
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "inline keyed")
}

// --- Non-telemetry constructs are ignored ---

// A struct that merely has an Attributes field is not a telemetry payload, so its
// keyed construction is ignored even in a file that imports a telemetry package.
func TestScanIgnoresUnrelatedStructs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.demo/telemetry.go", `package demo

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

type span struct{ Attributes map[string]string }

var _ = foundryTelemetry.Event{Attributes: map[string]string{"route": "inspector"}}
var _ = span{Attributes: map[string]string{"ignored": "value"}}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, diagnostics)
	require.Equal(t, []string{"route"}, usageKeys(usages))
}

func TestScanIgnoresTestFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.demo/telemetry_test.go", `package demo

import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

var _ = foundryTelemetry.Event{Attributes: map[string]string{"route": "inspector"}}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Empty(t, diagnostics)
}

// A same-named type in another package is a plain struct, not a payload alias, so
// its keyed construction is ignored.
func TestScanIgnoresCrossPackageNonPayloadType(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/go.mod", "module github.com/contoso/agent\n\ngo 1.24\n")
	writeExtensionSource(t, root, "contoso.agent/internal/shared/model.go", `package shared

type Usage struct {
	Attributes map[string]string
}
`)
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/report.go", `package cmd

import "github.com/contoso/agent/internal/shared"

func decorate(key string) {
	_ = shared.Usage{Attributes: map[string]string{key: "value"}}
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Empty(t, diagnostics)
}

// A chain of aliases whose base type is not a payload is ignored at every hop.
func TestScanIgnoresChainedNonPayloadAlias(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeExtensionSource(t, root, "contoso.agent/internal/cmd/telemetry.go", `package cmd

type config struct {
	Attributes map[string]string
}

type settings = config
type profile = settings

func decorate(key string) {
	_ = profile{Attributes: map[string]string{key: "value"}}
}
`)

	usages, diagnostics := scanExtensionTelemetry(root)

	require.Empty(t, usages)
	require.Empty(t, diagnostics)
}

func writeExtensionSource(t *testing.T, root, relativePath, content string) {
	t.Helper()

	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o600))
}

func usageKeys(usages []telemetryUsage) []string {
	keys := make([]string, 0, len(usages))
	for _, usage := range usages {
		keys = append(keys, usage.key)
	}
	sort.Strings(keys)
	return keys
}
