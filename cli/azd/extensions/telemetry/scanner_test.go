// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// The scanner discovers attribute keys by recognizing telemetry payload literals
// (foundry telemetry.Event and azdext ReportUsageRequest) wherever they appear in
// extension source, then reading their inline Attributes map. These tests pin the
// supported grammar and the fail-closed rejections.

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
	require.Contains(t, diagnostics[0], "assigning them after construction")
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

// A struct that merely has an Attributes field is not a telemetry payload, so its
// keys are ignored even in a file that imports a telemetry package.
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
