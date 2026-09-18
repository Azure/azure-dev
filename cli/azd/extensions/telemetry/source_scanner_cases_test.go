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

// Scanner cases document the supported source grammar and fail-closed behavior.
func TestExtensionTelemetrySourceScanner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		filename        string
		source          string
		expectedKeys    []string
		expectedMessage string
	}{
		{
			name:     "helper event builder",
			filename: "telemetry.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
const routeKey = "route"
func build(route string) foundryTelemetry.Event {
	return foundryTelemetry.Event{
		Name: "route.selected",
		Attributes: map[string]string{routeKey: route},
	}
}
`,
			expectedKeys: []string{"route"},
		},
		{
			name:     "direct report usage request",
			filename: "telemetry.go",
			source: `package telemetry
import host "github.com/azure/azure-dev/cli/azd/pkg/azdext"
var _ = host.ReportUsageRequest{
	Attributes: map[string]string{"outcome": "succeeded"},
}
`,
			expectedKeys: []string{"outcome"},
		},
		{
			name:     "versioned report usage request",
			filename: "telemetry.go",
			source: `package telemetry
import v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
var _ = v1beta.ReportUsageRequest{
	Attributes: map[string]string{"demo.mode": "sample"},
}
`,
			expectedKeys: []string{"demo.mode"},
		},
		{
			name:     "compile-time key expression",
			filename: "telemetry.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
const prefix = "deploy."
const key = prefix + "mode"
var _ = foundryTelemetry.Event{Attributes: map[string]string{key: "container"}}
`,
			expectedKeys: []string{"deploy.mode"},
		},
		{
			name:     "payload alias",
			filename: "telemetry.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
type event = foundryTelemetry.Event
var _ = event{Attributes: map[string]string{"route": "inspector"}}
`,
			expectedKeys: []string{"route"},
		},
		{
			name:     "type-elided payload",
			filename: "telemetry.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
var _ = []foundryTelemetry.Event{{
	Attributes: map[string]string{"route": "inspector"},
}}
`,
			expectedKeys: []string{"route"},
		},
		{
			name:     "generic container fails closed",
			filename: "telemetry.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
type batch[T any] = []T
var _ = batch[foundryTelemetry.Event]{{
	Name: "reported",
	Attributes: map[string]string{"route": "inspector"},
}}
`,
			expectedMessage: "generic composite literals are not supported",
		},
		{
			name:     "post-construction mutation fails closed",
			filename: "telemetry.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
func report() {
	event := foundryTelemetry.Event{Attributes: map[string]string{}}
	event.Attributes["route"] = "inspector"
}
`,
			expectedKeys:    []string{"route"},
			expectedMessage: "post-construction access is not supported",
		},
		{
			name:     "generated getter fails closed",
			filename: "telemetry.go",
			source: `package telemetry
import host "github.com/azure/azure-dev/cli/azd/pkg/azdext"
func report() {
	request := &host.ReportUsageRequest{Attributes: map[string]string{}}
	request.GetAttributes()["route"] = "inspector"
}
`,
			expectedMessage: "GetAttributes access is not supported",
		},
		{
			name:     "dynamic key is rejected",
			filename: "telemetry.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
var key = "route"
var _ = foundryTelemetry.Event{Attributes: map[string]string{key: "inspector"}}
`,
			expectedMessage: "must be a string literal or same-package compile-time string constant",
		},
		{
			name:     "attribute map variable is rejected",
			filename: "telemetry.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
var attributes = map[string]string{"route": "inspector"}
var _ = foundryTelemetry.Event{Attributes: attributes}
`,
			expectedMessage: "must be an inline map[string]string literal",
		},
		{
			name:     "unkeyed payload is rejected",
			filename: "telemetry.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
var _ = foundryTelemetry.Event{"reported", map[string]string{"route": "inspector"}}
`,
			expectedMessage: "payload literals must use keyed fields",
		},
		{
			name:     "shadowed nil is rejected",
			filename: "telemetry.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
func report(nil map[string]string) {
	_ = foundryTelemetry.Event{Attributes: nil}
}
`,
			expectedMessage: "must be an inline map[string]string literal",
		},
		{
			name:     "unrelated event type is ignored",
			filename: "telemetry.go",
			source: `package telemetry
type Event struct { Attributes map[string]string }
var _ = Event{Attributes: map[string]string{"ignored": "value"}}
`,
		},
		{
			name:     "test source is ignored",
			filename: "telemetry_test.go",
			source: `package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
var _ = foundryTelemetry.Event{Attributes: map[string]string{"ignored": "value"}}
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			extensionDir := filepath.Join(root, "contoso.extension")
			require.NoError(t, os.MkdirAll(extensionDir, 0o755))
			require.NoError(t, os.WriteFile(
				filepath.Join(extensionDir, test.filename),
				[]byte(test.source),
				0o600,
			))

			usages, diagnostics := scanExtensionTelemetry(root)
			var actualKeys []string
			for _, usage := range usages {
				actualKeys = append(actualKeys, usage.key)
			}
			sort.Strings(actualKeys)

			require.Equal(t, test.expectedKeys, actualKeys)
			if test.expectedMessage == "" {
				require.Empty(t, diagnostics)
			} else {
				require.NotEmpty(t, diagnostics)
				require.Contains(t, strings.Join(diagnostics, "\n"), test.expectedMessage)
			}
		})
	}
}

func TestExtensionTelemetrySourceScannerCrossFileBindings(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension")
	require.NoError(t, os.MkdirAll(extensionDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(extensionDir, "payload.go"),
		[]byte(`package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
var event = foundryTelemetry.Event{}
var attributes = event.Attributes
var nil = map[string]string{"undeclared": "value"}
`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(extensionDir, "usage.go"),
		[]byte(`package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
var _ = foundryTelemetry.Event{Attributes: nil}
func report() {
	event.Attributes["route"] = "inspector"
	attributes["outcome"] = "succeeded"
}
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	var actualKeys []string
	for _, usage := range usages {
		actualKeys = append(actualKeys, usage.key)
	}
	sort.Strings(actualKeys)

	require.Equal(t, []string{"outcome", "route"}, actualKeys)
	joinedDiagnostics := strings.Join(diagnostics, "\n")
	require.Contains(t, joinedDiagnostics, "must be an inline map[string]string literal")
	require.Contains(t, joinedDiagnostics, "post-construction access is not supported")
}

func TestExtensionTelemetrySourceScannerTracksNewValuePayload(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension")
	require.NoError(t, os.MkdirAll(extensionDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(extensionDir, "usage.go"),
		[]byte(`package telemetry
import "github.com/azure/azure-dev/cli/azd/pkg/azdext"
var dynamicKey = "route"
func report() {
	request := new(azdext.ReportUsageRequest{})
	request.Attributes[dynamicKey] = "inspector"
}
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	require.Empty(t, usages)
	joinedDiagnostics := strings.Join(diagnostics, "\n")
	require.Contains(t, joinedDiagnostics, "must be a string literal or same-package compile-time string constant")
	require.Contains(t, joinedDiagnostics, "post-construction access is not supported")
}

func TestExtensionTelemetrySourceScannerTracksSelectorFunctionReturns(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension")
	helperDir := filepath.Join(extensionDir, "internal", "helper")
	commandDir := filepath.Join(extensionDir, "internal", "cmd")
	require.NoError(t, os.MkdirAll(helperDir, 0o755))
	require.NoError(t, os.MkdirAll(commandDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(helperDir, "event.go"),
		[]byte(`package helper
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
func NewEvent() foundryTelemetry.Event {
	return foundryTelemetry.Event{}
}
`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(commandDir, "usage.go"),
		[]byte(`package cmd
import (
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"example.com/contoso.extension/internal/helper"
)
type eventFactory struct{}
func (eventFactory) NewEvent() foundryTelemetry.Event {
	return foundryTelemetry.Event{}
}
var packageKey = "package"
var methodKey = "method"
func report() {
	packageEvent := helper.NewEvent()
	packageEvent.Attributes[packageKey] = "value"
	factory := eventFactory{}
	methodEvent := factory.NewEvent()
	methodEvent.Attributes[methodKey] = "value"
}
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	require.Empty(t, usages)
	joinedDiagnostics := strings.Join(diagnostics, "\n")
	require.Equal(t, 2, strings.Count(
		joinedDiagnostics,
		"must be a string literal or same-package compile-time string constant",
	))
	require.Equal(t, 2, strings.Count(joinedDiagnostics, "post-construction access is not supported"))
}

func TestExtensionTelemetrySourceScannerCrossPackageAliases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension")
	wrapperDir := filepath.Join(extensionDir, "internal", "telemetry")
	consumerDir := filepath.Join(extensionDir, "internal", "cmd")
	require.NoError(t, os.MkdirAll(wrapperDir, 0o755))
	require.NoError(t, os.MkdirAll(consumerDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(wrapperDir, "event.go"),
		[]byte(`package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
type Event = foundryTelemetry.Event
`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(consumerDir, "usage.go"),
		[]byte(`package cmd
import extensionTelemetry "example.com/contoso.extension/internal/telemetry"
type localEvent struct {
	Name string
	Attributes map[string]string
}
type taggedEvent struct {
	Name string `+"`json:\"name\"`"+`
	Attributes map[string]string `+"`json:\"attributes\"`"+`
}
var _ = extensionTelemetry.Event{
	Name: "reported",
	Attributes: map[string]string{"route": "inspector"},
}
var _ = extensionTelemetry.Event(localEvent{
	Name: "reported",
	Attributes: map[string]string{"outcome": "succeeded"},
})
var _ = []extensionTelemetry.Event{{
	Name: "reported",
	Attributes: map[string]string{"container": "slice"},
}}
var _ = map[string]extensionTelemetry.Event{
	"reported": {
		Name: "reported",
		Attributes: map[string]string{"map": "value"},
	},
}
var _ = []*extensionTelemetry.Event{{
	"reported",
	map[string]string{"pointer": "slice"},
}}
var _ = map[string]*extensionTelemetry.Event{
	"reported": {
		"reported",
		map[string]string{"pointer": "map"},
	},
}
var _ = []extensionTelemetry.Event{{
	"reported",
	map[string]string{"unkeyed": "slice"},
}}
var _ = extensionTelemetry.Event(taggedEvent{
	"reported",
	map[string]string{"tagged": "conversion"},
})
var _ = extensionTelemetry.Event{
	"reported",
	map[string]string{"stage": "ready"},
}
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	var actualKeys []string
	for _, usage := range usages {
		actualKeys = append(actualKeys, usage.key)
	}
	sort.Strings(actualKeys)

	require.Equal(t, []string{"container", "map", "outcome", "route"}, actualKeys)
	require.Contains(
		t,
		strings.Join(diagnostics, "\n"),
		"payload literals must use keyed fields",
	)
}

func TestExtensionTelemetrySourceScannerPointerElidedContainers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension", "internal", "cmd")
	require.NoError(t, os.MkdirAll(extensionDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(extensionDir, "usage.go"),
		[]byte(`package cmd
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
var _ = []*[]foundryTelemetry.Event{{{
	"reported",
	map[string]string{"pointer.container.slice": "value"},
}}}
var _ = map[string]*[]foundryTelemetry.Event{
	"reported": {{
		"reported",
		map[string]string{"pointer.container.map": "value"},
	}},
}
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	require.Empty(t, usages)
	require.Len(t, diagnostics, 2)
	for _, diagnostic := range diagnostics {
		require.Contains(t, diagnostic, "extension telemetry payload literals must use keyed fields")
	}
}

func TestExtensionTelemetrySourceScannerRejectsCrossPackageNamedContainers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension")
	typesDir := filepath.Join(extensionDir, "internal", "telemetrytypes")
	consumerDir := filepath.Join(extensionDir, "internal", "cmd")
	require.NoError(t, os.MkdirAll(typesDir, 0o755))
	require.NoError(t, os.MkdirAll(consumerDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(typesDir, "types.go"),
		[]byte(`package telemetrytypes
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
type EventMap map[string]foundryTelemetry.Event
`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(consumerDir, "usage.go"),
		[]byte(`package cmd
import telemetryTypes "example.com/contoso.extension/internal/telemetrytypes"
var _ = telemetryTypes.EventMap{
	"reported": {
		"reported",
		map[string]string{"cross.package.container": "value"},
	},
}
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	require.Empty(t, usages)
	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], "unresolved unkeyed composite literals are not supported")
}

func TestExtensionTelemetrySourceScannerRejectsReexportedPayloadImportForms(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension")
	wrapperDir := filepath.Join(extensionDir, "internal", "wrapped")
	implicitDir := filepath.Join(extensionDir, "internal", "implicit")
	dotDir := filepath.Join(extensionDir, "internal", "dot")
	require.NoError(t, os.MkdirAll(wrapperDir, 0o755))
	require.NoError(t, os.MkdirAll(implicitDir, 0o755))
	require.NoError(t, os.MkdirAll(dotDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(wrapperDir, "event.go"),
		[]byte(`package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
type Event = foundryTelemetry.Event
`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(implicitDir, "usage.go"),
		[]byte(`package implicit
import "example.com/contoso.extension/internal/wrapped"
type localEvent telemetry.Event
var _ = telemetry.Event(localEvent{
	"reported",
	map[string]string{"implicit.reexport": "value"},
})
`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(dotDir, "usage.go"),
		[]byte(`package dot
import . "example.com/contoso.extension/internal/wrapped"
type localEvent Event
var _ = Event(localEvent{
	"reported",
	map[string]string{"dot.reexport": "value"},
})
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	require.Empty(t, usages)
	require.Len(t, diagnostics, 2)
	joinedDiagnostics := strings.Join(diagnostics, "\n")
	require.Contains(t, joinedDiagnostics, "unresolved unkeyed composite literals are not supported")
	require.Contains(t, joinedDiagnostics, "payload literals must use keyed fields")
}

func TestExtensionTelemetrySourceScannerRejectsCrossFilePredeclaredShadows(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension")
	typesDir := filepath.Join(extensionDir, "internal", "telemetrytypes")
	consumerDir := filepath.Join(extensionDir, "internal", "cmd")
	require.NoError(t, os.MkdirAll(typesDir, 0o755))
	require.NoError(t, os.MkdirAll(consumerDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(typesDir, "types.go"),
		[]byte(`package telemetrytypes
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
type EventMap map[string]foundryTelemetry.Event
`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(consumerDir, "values.go"),
		[]byte(`package cmd
type text = string
var true = map[string]string{"true.shadow": "value"}
var false = map[string]string{"false.shadow": "value"}
var string = func(text) map[text]text {
	return map[text]text{"string.shadow": "value"}
}
`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(consumerDir, "usage.go"),
		[]byte(`package cmd
import telemetryTypes "example.com/contoso.extension/internal/telemetrytypes"
var _ = telemetryTypes.EventMap{
	"true": {"reported", true},
}
var _ = telemetryTypes.EventMap{
	"false": {"reported", false},
}
var _ = telemetryTypes.EventMap{
	"string": {"reported", string("value")},
}
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	require.Empty(t, usages)
	require.Len(t, diagnostics, 3)
	for _, diagnostic := range diagnostics {
		require.Contains(t, diagnostic, "unresolved unkeyed composite literals are not supported")
	}
}

func TestExtensionTelemetrySourceScannerIgnoresUnrelatedNamedContainers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension", "internal", "cmd")
	require.NoError(t, os.MkdirAll(extensionDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(extensionDir, "headers.go"),
		[]byte(`package cmd
import (
	"net/textproto"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
)
var _ = foundryTelemetry.Event{Attributes: nil}
var _ = textproto.MIMEHeader{
	"Content-Disposition": {"form-data"},
	"Content-Type": {"application/json", "charset=utf-8"},
}
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	require.Empty(t, usages)
	require.Empty(t, diagnostics)
}

func TestExtensionTelemetrySourceScannerIgnoresExtensionsWithoutTelemetryPayloads(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension", "internal", "cmd")
	require.NoError(t, os.MkdirAll(extensionDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(extensionDir, "points.go"),
		[]byte(`package cmd
import (
	"image"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)
type page[T any] struct {
	Items []T
}
var _ = azdext.EmptyRequest{}
var _ = page[string]{Items: []string{"value"}}
var _ = []image.Point{{1, 2}}
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	require.Empty(t, usages)
	require.Empty(t, diagnostics)
}

func TestExtensionTelemetrySourceScannerDoesNotEnableUnrelatedPackages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	telemetryDir := filepath.Join(root, "contoso.extension", "internal", "telemetry")
	modelDir := filepath.Join(root, "contoso.extension", "internal", "model")
	require.NoError(t, os.MkdirAll(telemetryDir, 0o755))
	require.NoError(t, os.MkdirAll(modelDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(telemetryDir, "event.go"),
		[]byte(`package telemetry
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
var _ = foundryTelemetry.Event{Attributes: nil}
`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(modelDir, "model.go"),
		[]byte(`package model
import "github.com/azure/azure-dev/cli/azd/pkg/azdext"
type envelope[T any] struct {
	Attributes map[string]string
	Value T
}
func (e envelope[T]) GetAttributes() map[string]string {
	return e.Attributes
}
var value = envelope[string]{
	Attributes: map[string]string{"unrelated": "value"},
	Value: "value",
}
var _ = value.Attributes
var _ = value.GetAttributes()
var _ = azdext.EmptyRequest{}
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	require.Empty(t, usages)
	require.Empty(t, diagnostics)
}

func TestExtensionTelemetrySourceScannerIgnoresUnrelatedAttributesInTelemetryPackage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "contoso.extension", "internal", "cmd")
	require.NoError(t, os.MkdirAll(extensionDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(extensionDir, "usage.go"),
		[]byte(`package cmd
import foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
type envelope struct {
	Name string
	Attributes map[string]string
}
func (e envelope) GetAttributes() map[string]string {
	return e.Attributes
}
var value = envelope{
	Name: "unrelated",
	Attributes: map[string]string{"unrelated": "value"},
}
var _ = foundryTelemetry.Event{
	Attributes: map[string]string{"route": "inspector"},
}
var _ = value.Attributes
var _ = value.GetAttributes()
`),
		0o600,
	))

	usages, diagnostics := scanExtensionTelemetry(root)
	require.Equal(t, []telemetryUsage{{
		extension: "contoso.extension",
		key:       "route",
		path:      "contoso.extension/internal/cmd/usage.go",
		line:      15,
	}}, usages)
	require.Empty(t, diagnostics)
}
