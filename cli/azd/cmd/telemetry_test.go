// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

func TestTelemetryEventConstants(t *testing.T) {
	t.Parallel()
	require.Equal(t, "ext.update", events.ExtensionUpdateEvent)
}

// TestTelemetryFieldConstants verifies that all telemetry field constants added for
// command-specific instrumentation are properly defined and produce valid attribute
// key-value pairs. This is a contract test: if a field constant is removed or renamed,
// this test will fail, catching regressions in the telemetry schema.
//
// NOTE: This test validates field definitions, not command-level instrumentation.
// Command-level coverage is enforced via the documented allowlist in
// TestCommandTelemetryCoverageAllowlist (below) and the feature-telemetry-matrix.md.
// Raw attribute.* string-literal keys in telemetry sinks are additionally
// rejected by TestNoRawTelemetryAttributes (below).
func TestTelemetryFieldConstants(t *testing.T) {
	t.Parallel()
	// Auth command telemetry fields
	t.Run("AuthFields", func(t *testing.T) {
		t.Parallel()
		kv := fields.AuthMethodKey.String("browser")
		require.Equal(t, "auth.method", string(kv.Key))
		require.Equal(t, "browser", kv.Value.AsString())

		// Verify all auth method values are valid strings
		authMethods := []string{
			"browser", "device-code", "service-principal-secret",
			"service-principal-certificate", "federated-github",
			"federated-azure-pipelines", "federated-oidc",
			"managed-identity", "external", "oneauth",
		}
		for _, method := range authMethods {
			kv := fields.AuthMethodKey.String(method)
			require.NotEmpty(t, kv.Value.AsString())
		}

		// Cache-clear failure indicator (fixed enum, emitted on `auth login`).
		kvCache := fields.AuthCacheClearFailedKey.String("auth")
		require.Equal(t, "auth.cache_clear_failed", string(kvCache.Key))
		require.Equal(t, "auth", kvCache.Value.AsString())
		for _, which := range []string{"auth", "subscriptions"} {
			kv := fields.AuthCacheClearFailedKey.String(which)
			require.NotEmpty(t, kv.Value.AsString())
		}
	})

	// Env command telemetry fields
	t.Run("EnvFields", func(t *testing.T) {
		t.Parallel()
		// Env count is a measurement
		kvCount := fields.EnvCountKey.Int(3)
		require.Equal(t, "env.count", string(kvCount.Key))
		require.Equal(t, int64(3), kvCount.Value.AsInt64())
	})

	// Hooks command telemetry fields
	t.Run("HooksFields", func(t *testing.T) {
		t.Parallel()
		kv := fields.HooksNameKey.String("predeploy")
		require.Equal(t, "hooks.name", string(kv.Key))

		kvType := fields.HooksTypeKey.String("project")
		require.Equal(t, "hooks.type", string(kvType.Key))
	})

	// Pipeline command telemetry fields
	t.Run("PipelineFields", func(t *testing.T) {
		t.Parallel()
		kv := fields.PipelineProviderKey.String("github")
		require.Equal(t, "pipeline.provider", string(kv.Key))

		kvAuth := fields.PipelineAuthKey.String("federated")
		require.Equal(t, "pipeline.auth", string(kvAuth.Key))
	})

	// Infra command telemetry fields
	t.Run("InfraFields", func(t *testing.T) {
		t.Parallel()
		providers := []string{"bicep", "terraform"}
		for _, provider := range providers {
			kv := fields.InfraProviderKey.String(provider)
			require.Equal(t, "infra.provider", string(kv.Key))
			require.Equal(t, provider, kv.Value.AsString())
		}
	})

	// Tool command telemetry fields
	t.Run("ToolFields", func(t *testing.T) {
		t.Parallel()

		// First-run experience fields
		kvSkip := fields.ToolFirstRunSkipReasonKey.String("ci_cd")
		require.Equal(t, "tool.firstrun.skip_reason", string(kvSkip.Key))
		require.Equal(t, "ci_cd", kvSkip.Value.AsString())

		kvOptIn := fields.ToolFirstRunOptInKey.Bool(true)
		require.Equal(t, "tool.firstrun.opt_in", string(kvOptIn.Key))
		require.Equal(t, true, kvOptIn.Value.AsBool())

		kvDetected := fields.ToolFirstRunToolsDetectedKey.Int(5)
		require.Equal(t, "tool.firstrun.tools_detected", string(kvDetected.Key))
		require.Equal(t, int64(5), kvDetected.Value.AsInt64())

		kvOffered := fields.ToolFirstRunToolsOfferedKey.Int(3)
		require.Equal(t, "tool.firstrun.tools_offered", string(kvOffered.Key))

		kvSelected := fields.ToolFirstRunToolsSelectedKey.Int(2)
		require.Equal(t, "tool.firstrun.tools_selected", string(kvSelected.Key))

		kvSelectedNames := fields.ToolFirstRunToolsSelectedNamesKey.String("kubectl,helm")
		require.Equal(t, "tool.firstrun.tools_selected_names", string(kvSelectedNames.Key))

		kvDeselectedNames := fields.ToolFirstRunToolsDeselectedNamesKey.String("docker")
		require.Equal(t, "tool.firstrun.tools_deselected_names", string(kvDeselectedNames.Key))

		kvOutcome := fields.ToolFirstRunOutcomeKey.String("completed")
		require.Equal(t, "tool.firstrun.outcome", string(kvOutcome.Key))
		require.Equal(t, "completed", kvOutcome.Value.AsString())

		// Per-operation fields
		kvID := fields.ToolIdKey.String("kubectl")
		require.Equal(t, "tool.id", string(kvID.Key))
		require.Equal(t, "kubectl", kvID.Value.AsString())

		kvIDs := fields.ToolIdsKey.String("kubectl,helm")
		require.Equal(t, "tool.ids", string(kvIDs.Key))

		kvDryRun := fields.ToolDryRunKey.Bool(true)
		require.Equal(t, "tool.dry_run", string(kvDryRun.Key))

		kvStrategy := fields.ToolInstallStrategyKey.String("winget")
		require.Equal(t, "tool.install.strategy", string(kvStrategy.Key))

		kvSuccess := fields.ToolInstallSuccessKey.Bool(true)
		require.Equal(t, "tool.install.success", string(kvSuccess.Key))

		kvSuccessCount := fields.ToolInstallSuccessCountKey.Int(2)
		require.Equal(t, "tool.install.success_count", string(kvSuccessCount.Key))

		kvFailureCount := fields.ToolInstallFailureCountKey.Int(1)
		require.Equal(t, "tool.install.failure_count", string(kvFailureCount.Key))

		kvFailedIDs := fields.ToolInstallFailedIdsKey.String("kubectl")
		require.Equal(t, "tool.install.failed_ids", string(kvFailedIDs.Key))

		kvDuration := fields.ToolInstallDurationMsKey.Int64(1234)
		require.Equal(t, "tool.install.duration_ms", string(kvDuration.Key))
		require.Equal(t, int64(1234), kvDuration.Value.AsInt64())

		kvFRInstallSuccessCount := fields.ToolFirstRunInstallSuccessCountKey.Int(2)
		require.Equal(t, "tool.firstrun.install_success_count", string(kvFRInstallSuccessCount.Key))

		kvFRInstallFailureCount := fields.ToolFirstRunInstallFailureCountKey.Int(1)
		require.Equal(t, "tool.firstrun.install_failure_count", string(kvFRInstallFailureCount.Key))

		kvFRInstallFailedIDs := fields.ToolFirstRunInstallFailedIdsKey.String("kubectl")
		require.Equal(t, "tool.firstrun.install_failed_ids", string(kvFRInstallFailedIDs.Key))

		kvFRInstallDuration := fields.ToolFirstRunInstallDurationMsKey.Int64(1234)
		require.Equal(t, "tool.firstrun.install_duration_ms", string(kvFRInstallDuration.Key))

		kvFromVer := fields.ToolUpdateFromVersionKey.String("1.0.0")
		require.Equal(t, "tool.update.from_version", string(kvFromVer.Key))

		kvToVer := fields.ToolUpdateToVersionKey.String("1.1.0")
		require.Equal(t, "tool.update.to_version", string(kvToVer.Key))

		kvUpdates := fields.ToolCheckUpdatesAvailableKey.Int(3)
		require.Equal(t, "tool.check.updates_available", string(kvUpdates.Key))
	})

	t.Run("ExtensionFields", func(t *testing.T) {
		t.Parallel()
		kv := fields.ExtensionSourceKind.String("location")
		require.Equal(t, "extension.source.kind", string(kv.Key))
		require.Equal(t, "location", kv.Value.AsString())

		kv = fields.ExtensionEvent.String("deploy.completed")
		require.Equal(t, "extension.event", string(kv.Key))
		require.Equal(t, "deploy.completed", kv.Value.AsString())

		category := fields.ExtensionSourceCategory.String("local")
		require.Equal(t, "extension.source.category", string(category.Key))

		categoryFrom := fields.ExtensionSourceCategoryFrom.String("dev")
		require.Equal(t, "extension.source.category.from", string(categoryFrom.Key))

		categoryTo := fields.ExtensionSourceCategoryTo.String("azd")
		require.Equal(t, "extension.source.category.to", string(categoryTo.Key))

		installedCategories := fields.ExtensionsInstalledSourceCategories.StringSlice(
			[]string{"example@local"},
		)
		require.Equal(t, "extension.installed.source.category", string(installedCategories.Key))

		duration := fields.ExtensionUpdateDurationMs.Int64(1234)
		require.Equal(t, "extension.update.duration_ms", string(duration.Key))

		outcome := fields.ExtensionUpdateOutcome.String("upgraded")
		require.Equal(t, "extension.update.outcome", string(outcome.Key))

		dependencyCount := fields.ExtensionDependencyUpdateCount.Int(2)
		require.Equal(t, "extension.dependency_update_count", string(dependencyCount.Key))
	})

	// Provision validation telemetry fields (emitted by both the Bicep
	// "arm-provision" dispatch and the provider-agnostic "provision" dispatch).
	t.Run("ProvisionValidationFields", func(t *testing.T) {
		t.Parallel()

		kvOutcome := fields.ProvisionValidationOutcomeKey.String("passed")
		require.Equal(t, "validation.provision.outcome", string(kvOutcome.Key))
		require.Equal(t, "passed", kvOutcome.Value.AsString())

		kvDiagnostics := fields.ProvisionValidationDiagnosticsKey.StringSlice([]string{"BCP081"})
		require.Equal(t, "validation.provision.diagnostics", string(kvDiagnostics.Key))
		require.Equal(t, []string{"BCP081"}, kvDiagnostics.Value.AsStringSlice())

		kvRules := fields.ProvisionValidationRulesKey.StringSlice([]string{"rule-a"})
		require.Equal(t, "validation.provision.rules", string(kvRules.Key))
		require.Equal(t, []string{"rule-a"}, kvRules.Value.AsStringSlice())

		kvExtensionRules := fields.ProvisionValidationExtensionRulesKey.StringSlice([]string{"ext-rule"})
		require.Equal(t, "validation.provision.extension_rules", string(kvExtensionRules.Key))
		require.Equal(t, []string{"ext-rule"}, kvExtensionRules.Value.AsStringSlice())

		kvCheckType := fields.ProvisionValidationCheckTypeKey.String("provision")
		require.Equal(t, "validation.provision.check_type", string(kvCheckType.Key))
		require.Equal(t, "provision", kvCheckType.Value.AsString())

		kvWarnings := fields.ProvisionValidationWarningCountKey.Int(2)
		require.Equal(t, "validation.provision.warning.count", string(kvWarnings.Key))
		require.Equal(t, int64(2), kvWarnings.Value.AsInt64())

		kvErrors := fields.ProvisionValidationErrorCountKey.Int(1)
		require.Equal(t, "validation.provision.error.count", string(kvErrors.Key))
		require.Equal(t, int64(1), kvErrors.Value.AsInt64())
	})

	// Aspire telemetry fields
	t.Run("AspireFields", func(t *testing.T) {
		t.Parallel()
		kv := fields.AspireAppHostLanguageKey.String("typescript")
		require.Equal(t, "aspire.apphost.language", string(kv.Key))
		require.Equal(t, "typescript", kv.Value.AsString())

		for _, language := range []string{"typescript", "python", "go", "java", "rust"} {
			kv := fields.AspireAppHostLanguageKey.String(language)
			require.NotEmpty(t, kv.Value.AsString())
		}
	})

	// Container publish telemetry fields
	t.Run("ContainerFields", func(t *testing.T) {
		t.Parallel()
		kv := fields.ContainerRemoteBuildKey.Bool(true)
		require.Equal(t, "container.remotebuild", string(kv.Key))
		require.Equal(t, true, kv.Value.AsBool())
	})

	// AKS service target telemetry fields
	t.Run("AksFields", func(t *testing.T) {
		t.Parallel()
		kv := fields.AksSkipReasonKey.String("cluster_not_provisioned")
		require.Equal(t, "skip.reason", string(kv.Key))
		require.Equal(t, "cluster_not_provisioned", kv.Value.AsString())
	})
}

// TestNoRawTelemetryAttributes enforces that product code never emits telemetry
// via a raw attribute constructor — attribute.String(key, ...), attribute.Bool,
// the chained attribute.Key(key).String(...) form, or a KeyValue-producing method
// called on any value of type attribute.Key. Every telemetry attribute must be
// declared as a fields.AttributeKey (with a Classification and Purpose) and
// emitted through it, e.g. fields.SomeKey.String(value). This keeps the telemetry
// schema discoverable and classifiable for the GDPR metadata pipeline (azure-dev
// issue #1803).
//
// The scan is type-aware (go/types via go/packages) rather than purely
// syntactic. Type information is required for soundness: the sanctioned
// fields.SomeKey.String(v) is a promoted method whose receiver is the classified
// fields.AttributeKey struct, which is AST-indistinguishable from a bare
// attribute.Key value's method call. Only the resolved types tell the two named
// types apart, and they also let the guard follow keys reached through import
// aliases, dot imports, struct fields, or function results.
//
// Legitimately excluded from the scan:
//   - *_test.go files (test fixtures build raw attributes on purpose): Tests is
//     false, so go/packages does not load them.
//   - Nested modules (extensions/*, test/evals, test data samples) have their own
//     go.mod and are not matched by the "./..." pattern. Extension telemetry is
//     out of scope by design, not merely by mechanics: extensions are separate
//     modules whose attributes (the "ext.*" namespace) are reviewed together with
//     the extension that reports them, per docs/specs/metrics-audit/
//     privacy-review-checklist.md, rather than against the core fields catalog.
func TestNoRawTelemetryAttributes(t *testing.T) {
	t.Parallel()

	// The test runs with its package directory (cli/azd/cmd) as the working
	// directory, so the module root is one level up.
	azdRoot, err := filepath.Abs("..")
	require.NoError(t, err)

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir:   azdRoot,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	require.NoError(t, err)
	require.NotEmpty(t, pkgs, "no packages loaded from the cli/azd module")

	var (
		violations []string
		loadErrors []string
	)
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			loadErrors = append(loadErrors, fmt.Sprintf("  %s: %s", pkg.PkgPath, e.Error()))
		}
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			filename := pkg.Fset.Position(file.Pos()).Filename
			rel, relErr := filepath.Rel(azdRoot, filename)
			if relErr != nil {
				rel = filename
			}
			rel = filepath.ToSlash(rel)
			violations = append(violations, scanFileForRawAttributes(pkg.Fset, file, pkg.TypesInfo, rel)...)
		}
	}

	// A package that failed to type-check would silently hide violations, so a
	// load error is a failure rather than a false pass.
	require.Empty(t, loadErrors, "packages failed to load/type-check:\n%s", strings.Join(loadErrors, "\n"))

	if len(violations) > 0 {
		t.Errorf(
			"Found %d raw telemetry attribute(s) constructed directly.\n"+
				"Declare an exported fields.AttributeKey (with Classification and Purpose) in\n"+
				"internal/tracing/fields/fields.go and emit via it, e.g. fields.MyKey.String(v),\n"+
				"so the property is discoverable and classifiable by the GDPR metadata pipeline.\n\n"+
				"Raw attributes:\n%s",
			len(violations),
			strings.Join(violations, "\n"),
		)
	}
}

// rawAttributePkgPath is the import path of the OpenTelemetry attribute package
// whose constructors and Key methods bypass the fields.AttributeKey registry.
const rawAttributePkgPath = "go.opentelemetry.io/otel/attribute"

// isRawAttributeKeyType reports whether t is exactly the
// go.opentelemetry.io/otel/attribute.Key named type. A struct that merely embeds
// it — such as the sanctioned fields.AttributeKey — is a different named type and
// returns false, which is what keeps the guard from flagging fields.SomeKey.String.
func isRawAttributeKeyType(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Pkg() != nil &&
		obj.Pkg().Path() == rawAttributePkgPath && obj.Name() == "Key"
}

// fieldsPkgPath is the import path of the package that defines the sanctioned
// classified attribute wrapper, fields.AttributeKey.
const fieldsPkgPath = "github.com/azure/azure-dev/cli/azd/internal/tracing/fields"

// isFieldsAttributeKeyType reports whether t is exactly the sanctioned
// fields.AttributeKey named type. That wrapper is the only type the metadata
// classifier discovers and reads Classification/Purpose/Endpoint from, so it is
// the only receiver on which a KeyValue-producing attribute.Key method is
// allowed. A bare attribute.Key, or any other struct that merely embeds
// attribute.Key, produces a key the classifier cannot see and is a violation.
func isFieldsAttributeKeyType(t types.Type) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Pkg() != nil &&
		obj.Pkg().Path() == fieldsPkgPath && obj.Name() == "AttributeKey"
}

// isAttributeKeyBuilderMethod reports whether m is a method defined on
// attribute.Key. The check is on the method's defining receiver type, so it
// matches whether the method is invoked directly on an attribute.Key value or
// promoted through an embedding struct, and it never matches an unrelated
// String()/Stringer() method on some other type. Callers additionally restrict
// to the KeyValue-producing names (rawAttributeConstructors) so attribute.Key's
// non-builder methods (e.g. Defined) are not flagged.
func isAttributeKeyBuilderMethod(m *types.Func) bool {
	sig, ok := m.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return false
	}
	return isRawAttributeKeyType(sig.Recv().Type())
}

// scanFileForRawAttributes returns the raw-telemetry-attribute violations in a
// single type-checked Go file. info must be the go/types information for the
// file's package; rel is the display path used in messages. Classifying calls by
// the resolved type of the callee and receiver — rather than by syntactic shape —
// makes the guard sound across import aliases, dot imports, and keys reached
// through parameters, struct fields, or function results. It is shared by
// TestNoRawTelemetryAttributes (which walks the module) and the fixture test
// TestRawTelemetryAttributeScanner, so the guard's contract is itself tested.
func scanFileForRawAttributes(fset *token.FileSet, file *ast.File, info *types.Info, rel string) []string {
	// rawAttributeConstructors are the go.opentelemetry.io/otel/attribute helpers
	// (and the identically named attribute.Key methods) that build a KeyValue from
	// a key and a value. Using them directly in product code bypasses the
	// fields.AttributeKey registry, so the GDPR metadata exporter (which discovers
	// only exported AttributeKey vars) can never classify the resulting property.
	// See docs/specs/metrics-audit/telemetry-schema.md.
	rawAttributeConstructors := map[string]struct{}{
		"String": {}, "Bool": {}, "Int": {}, "IntSlice": {}, "Int64": {},
		"Float64": {}, "Stringer": {}, "StringSlice": {}, "BoolSlice": {},
		"Int64Slice": {}, "Float64Slice": {},
	}

	// literalKey renders the first string-literal argument of a call for a
	// friendlier message, or "..." for a non-literal (e.g. a named constant).
	literalKey := func(c *ast.CallExpr) string {
		if len(c.Args) > 0 {
			if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				return lit.Value
			}
		}
		return "..."
	}

	// isRawAttributeConstructorFunc reports whether obj is a package-level function
	// of the attribute package that builds a KeyValue from a key and value (e.g.
	// attribute.String). Resolving via the types object makes this independent of
	// how the package was imported: default name, alias, or dot import.
	isRawAttributeConstructorFunc := func(obj types.Object) bool {
		fn, ok := obj.(*types.Func)
		if !ok || fn.Pkg() == nil || fn.Pkg().Path() != rawAttributePkgPath {
			return false
		}
		sig, ok := fn.Type().(*types.Signature)
		if !ok || sig.Recv() != nil {
			return false
		}
		_, isCtor := rawAttributeConstructors[fn.Name()]
		return isCtor
	}

	// isReemittedKeyValueKey reports whether expr is `<kv>.Key` where <kv> has type
	// attribute.KeyValue — i.e. a method call like kv.Key.String(v) merely re-emits
	// the key of a KeyValue that was already built (and, at its build site, already
	// subject to this guard). The telemetry baggage plumbing in
	// internal/tracing legitimately rebuilds caller-supplied KeyValues with merged
	// values this way; it introduces no new key literal, so it is not a violation.
	isReemittedKeyValueKey := func(expr ast.Expr) bool {
		sel, ok := expr.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Key" {
			return false
		}
		named, ok := info.TypeOf(sel.X).(*types.Named)
		if !ok {
			return false
		}
		obj := named.Obj()
		return obj != nil && obj.Pkg() != nil &&
			obj.Pkg().Path() == rawAttributePkgPath && obj.Name() == "KeyValue"
	}

	var violations []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		pos := fset.Position(call.Pos())

		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			// A method-value selection (x.String(v)) — including a method promoted
			// through embedding — resolves both the method's defining type and the
			// receiver expression's type. A KeyValue-producing attribute.Key method
			// is sanctioned only when the receiver is the classified
			// fields.AttributeKey wrapper (the sole type the metadata classifier
			// discovers). Called on a bare attribute.Key, or on any other struct
			// that merely embeds attribute.Key, it produces an unclassified key and
			// is a violation. This single check covers keys held in parameters,
			// locals, struct fields, and function results, as well as the chained
			// attribute.Key("k").String(v) form. Re-emitting an existing KeyValue's
			// key (kv.Key.String(v)) is excluded because the key was already checked
			// where the KeyValue was built.
			if sel := info.Selections[fun]; sel != nil && sel.Kind() == types.MethodVal {
				if m, ok := sel.Obj().(*types.Func); ok {
					if _, isCtor := rawAttributeConstructors[m.Name()]; isCtor &&
						isAttributeKeyBuilderMethod(m) &&
						!isFieldsAttributeKeyType(sel.Recv()) && !isReemittedKeyValueKey(fun.X) {
						violations = append(violations, fmt.Sprintf(
							"  %s:%d: .%s(...) produces an unclassified key "+
								"(receiver is not fields.AttributeKey)", rel, pos.Line, m.Name()))
					}
				}
				return true
			}
			// Otherwise the selector is a qualified identifier for a package-level
			// constructor, e.g. attribute.String("k", v) (possibly via an alias).
			if isRawAttributeConstructorFunc(info.Uses[fun.Sel]) {
				violations = append(violations, fmt.Sprintf(
					"  %s:%d: attribute.%s(%s, ...)", rel, pos.Line, fun.Sel.Name, literalKey(call)))
			}
		case *ast.Ident:
			// A bare identifier call resolves to a package-level constructor only
			// when the attribute package is dot-imported, e.g. String("k", v).
			if isRawAttributeConstructorFunc(info.Uses[fun]) {
				violations = append(violations, fmt.Sprintf(
					"  %s:%d: %s(%s, ...) (dot-imported attribute constructor)",
					rel, pos.Line, fun.Name, literalKey(call)))
			}
		}
		return true
	})

	return violations
}

// TestRawTelemetryAttributeScanner is a fixture test for the type-aware guard
// used by TestNoRawTelemetryAttributes. It pins the contract: raw attribute
// constructors (default, aliased, and dot-imported), constant keys, the chained
// attribute.Key(k).String(v) form, and KeyValue-producing method calls on a raw
// attribute.Key value reached through a parameter, a local, a struct field, or a
// function result are all flagged; while the sanctioned promoted-method call on a
// struct that embeds attribute.Key, a bare attribute.Key(k) conversion (which
// does not build a KeyValue), and non-KeyValue uses of a key (e.g. a map lookup)
// are not. Fixtures are type-checked against the real attribute package via
// go/packages, so the guard runs with the same type information it uses on the
// module.
func TestRawTelemetryAttributeScanner(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs("..")
	require.NoError(t, err)

	cases := []struct {
		name          string
		src           string
		wantViolation bool
	}{
		{
			name: "literal key",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
var _ = attribute.String("raw.key", "v")
`,
			wantViolation: true,
		},
		{
			name: "constant key",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
const k = "raw.key"
var _ = attribute.String(k, "v")
`,
			wantViolation: true,
		},
		{
			name: "aliased import",
			src: `package p
import otelattr "go.opentelemetry.io/otel/attribute"
var _ = otelattr.Bool("raw.key", true)
`,
			wantViolation: true,
		},
		{
			name: "dot-imported constructor",
			src: `package p
import . "go.opentelemetry.io/otel/attribute"
var _ = String("raw.key", "v")
`,
			wantViolation: true,
		},
		{
			name: "int slice constructor",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
var _ = attribute.IntSlice("raw.key", []int{1})
`,
			wantViolation: true,
		},
		{
			name: "chained key method",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
var _ = attribute.Key("raw.key").String("v")
`,
			wantViolation: true,
		},
		{
			name: "method on key-typed parameter",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
func f(k attribute.Key) { _ = k.String("v") }
`,
			wantViolation: true,
		},
		{
			name: "method on locally built key value",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
func f() { k := attribute.Key("raw.key"); _ = k.String("v") }
`,
			wantViolation: true,
		},
		{
			name: "method on inferred key declaration",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
var k = attribute.Key("raw.key")
var _ = k.String("v")
`,
			wantViolation: true,
		},
		{
			name: "method on struct-field key",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
type holder struct{ key attribute.Key }
func f(h holder) { _ = h.key.String("v") }
`,
			wantViolation: true,
		},
		{
			name: "method on function-result key",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
func mk() attribute.Key { return attribute.Key("raw.key") }
func f() { _ = mk().String("v") }
`,
			wantViolation: true,
		},
		{
			name: "method via embedded Key field of a struct",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
type wrapper struct{ attribute.Key }
func f(c wrapper) { _ = c.Key.Bool(true) }
`,
			wantViolation: true,
		},
		{
			name: "reemit method on KeyValue key field",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
func f(kv attribute.KeyValue) { _ = kv.Key.String("v") }
`,
			wantViolation: false,
		},
		{
			name: "bare key conversion without value",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
var _ = attribute.Key("raw.key")
`,
			wantViolation: false,
		},
		{
			name: "promoted method on non-fields embedding struct",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
type wrapper struct{ attribute.Key }
var c wrapper
var _ = c.String("v")
`,
			wantViolation: true,
		},
		{
			name: "promoted method on classified fields.AttributeKey",
			src: `package p
import "github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
var _ = fields.ServiceNameKey.String("v")
`,
			wantViolation: false,
		},
		{
			name: "dynamic sanctioned extension usage attribute",
			src: `package p
import "github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
var _ = fields.ExtensionUsageAttribute("foo").String("v")
`,
			wantViolation: false,
		},
		{
			name: "shadowed classified key across scopes",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
import "github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
func a(k attribute.Key) { _ = k }
func b(k fields.AttributeKey) { _ = k.Bool(true) }
`,
			wantViolation: false,
		},
		{
			name: "map lookup on key-typed parameter",
			src: `package p
import "go.opentelemetry.io/otel/attribute"
func f(m map[attribute.Key]int, k attribute.Key) int { return m[k] }
`,
			wantViolation: false,
		},
		{
			name: "file without the attribute import",
			src: `package p
var _ = 1
`,
			wantViolation: false,
		},
	}

	// Type-check every fixture against the real attribute package via an in-memory
	// overlay. The virtual files live under a directory that does not exist on
	// disk, so they neither collide with the module walk nor require cleanup.
	overlay := make(map[string][]byte, len(cases))
	patterns := make([]string, 0, len(cases))
	pathToCase := make(map[string]int, len(cases))
	for i, tc := range cases {
		p := filepath.Join(root, "cmd", fmt.Sprintf("zz_rawscan_fixture_%02d", i), "fixture.go")
		overlay[p] = []byte(tc.src)
		patterns = append(patterns, "file="+p)
		pathToCase[filepath.ToSlash(p)] = i
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir:     root,
		Overlay: overlay,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	require.NoError(t, err)

	got := make([]bool, len(cases))
	checked := make([]bool, len(cases))
	for _, pkg := range pkgs {
		require.Empty(t, pkg.Errors, "fixture package %s failed to type-check", pkg.PkgPath)
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			fname := filepath.ToSlash(pkg.Fset.Position(file.Pos()).Filename)
			idx, ok := pathToCase[fname]
			if !ok {
				continue
			}
			got[idx] = len(scanFileForRawAttributes(pkg.Fset, file, pkg.TypesInfo, cases[idx].name)) > 0
			checked[idx] = true
		}
	}

	for i, tc := range cases {
		require.True(t, checked[i], "fixture %q was not loaded/type-checked", tc.name)
		require.Equal(t, tc.wantViolation, got[i], "fixture %q: unexpected violation result", tc.name)
	}
}

// TestCommandTelemetryCoverage ensures every user-facing command is explicitly categorized
// for telemetry coverage. When a new command is added to the CLI, it must be added to one
// of the lists below. This forces developers to consciously decide whether the command needs
// command-specific telemetry attributes or whether global middleware telemetry is sufficient.
//
// NOTE: Building the full command tree via NewRootCmd requires the DI container, which makes
// it impractical for a unit test. Instead, we maintain an explicit manifest of all known
// user-facing commands and their telemetry classification. This test fails if:
//   - A command appears in both lists (contradictory classification)
//   - A command appears in neither list (unclassified — forces developer action)
//   - The lists are not sorted (maintainability)
func TestCommandTelemetryCoverage(t *testing.T) {
	t.Parallel()
	// Commands that have command-specific telemetry attributes emitted via
	// tracing.SetUsageAttributes (beyond the global middleware that tracks
	// command name, flags, duration, and errors for all commands).
	//
	// When adding a command here, ensure the command's action sets at least one
	// command-specific attribute (e.g., auth.method, config.operation, env.operation).
	commandsWithSpecificTelemetry := []string{
		"auth login",        // auth.method
		"build",             // (via hooks middleware)
		"deploy",            // service attributes (via hooks middleware)
		"down",              // infra.provider (resolved provider, via provisioning manager)
		"env list",          // env.count
		"extension install", // extension.source.kind
		"extension list",    // extension.source.kind
		"extension show",    // extension.source.kind
		// extension.source.category
		"extension source add",
		"extension update", // extension.source.kind + extension update spans
		"hooks run",        // hooks.name, hooks.type
		"infra generate",   // infra.provider
		"init",             // init.method, appinit.* fields
		"package",          // (via hooks middleware)
		"pipeline config",  // pipeline.provider, pipeline.auth
		"provision",        // infra.provider (resolved provider, via provisioning manager)
		"restore",          // (via hooks middleware)
		"tool check",       // tool.check.updates_available
		"tool install",     // tool.id(s), tool.dry_run, tool.install.* aggregate + per-tool fields
		"tool show",        // tool.id
		"tool uninstall",   // tool.id(s), tool.dry_run, tool.install.* aggregate + per-tool fields
		"tool update",      // tool.id(s), tool.dry_run, tool.install.* aggregate + tool.update.* versions
		"up",               // infra.provider (via provisioning manager; composes provision+deploy)
		"update",           // update.* fields
	}

	// Commands that rely ONLY on global middleware telemetry (command name, flags,
	// duration, errors) and do NOT emit command-specific attributes. Each entry
	// includes a justification for why command-specific telemetry is not needed.
	commandsWithOnlyGlobalTelemetry := []string{
		"auth logout",       // No command-specific telemetry — logout is a simple operation
		"auth status",       // Global telemetry sufficient — auth check is simple pass/fail
		"completion",        // Shell completion script generation — no meaningful usage signal
		"config get",        // Global telemetry sufficient — low cardinality
		"config list",       // Global telemetry sufficient — low cardinality
		"config list-alpha", // Simple list of alpha features — no operational variance
		"config reset",      // Global telemetry sufficient — low cardinality
		"config set",        // Global telemetry sufficient — low cardinality
		"config show",       // Global telemetry sufficient — low cardinality
		"config unset",      // Global telemetry sufficient — low cardinality
		"copilot",           // Copilot session telemetry handled by copilot.* fields at session level
		"env config get",    // Thin wrapper — low cardinality, global telemetry sufficient
		"env config set",    // Thin wrapper — low cardinality, global telemetry sufficient
		"env config unset",  // Thin wrapper — low cardinality, global telemetry sufficient
		"env get-value",     // Global telemetry sufficient — command name captures operation
		"env get-values",    // Global telemetry sufficient — command name captures operation
		"env new",           // Global telemetry sufficient — command name captures operation
		"env refresh",       // Global telemetry sufficient — command name captures operation
		"env remove",        // Destructive but simple — global telemetry captures usage
		"env select",        // Global telemetry sufficient — command name captures operation
		"env set",           // Global telemetry sufficient — command name captures operation
		"env set-secret",    // Global telemetry sufficient — command name captures operation
		// Global telemetry is sufficient because configured values are not emitted.
		"extension source list",
		"extension source remove",
		"extension source validate",
		"mcp",                    // MCP tool telemetry handled by mcp.* fields at invocation level
		"monitor",                // Global telemetry sufficient — command name captures usage
		"show",                   // Global telemetry sufficient — output format not analytically useful
		"telemetry",              // Meta-command for telemetry itself — avoid recursion
		"template list",          // Global telemetry sufficient — command name captures operation
		"template show",          // Global telemetry sufficient — command name captures operation
		"template source add",    // Global telemetry sufficient — command name captures operation
		"template source list",   // Global telemetry sufficient — command name captures operation
		"template source remove", // Global telemetry sufficient — command name captures operation
		"tool",                   // Parent group — no operation-specific telemetry
		"tool list",              // Listing tool registry — global telemetry sufficient
		"version",                // Telemetry explicitly disabled (DisableTelemetry: true)
		"vs-server",              // JSON-RPC server — telemetry handled by rpc.* fields per call
	}

	// Build lookup maps
	specificMap := make(map[string]bool, len(commandsWithSpecificTelemetry))
	for _, cmd := range commandsWithSpecificTelemetry {
		specificMap[cmd] = true
	}

	globalOnlyMap := make(map[string]bool, len(commandsWithOnlyGlobalTelemetry))
	for _, cmd := range commandsWithOnlyGlobalTelemetry {
		globalOnlyMap[cmd] = true
	}

	// Verify no command appears in both lists
	for _, cmd := range commandsWithSpecificTelemetry {
		require.False(t, globalOnlyMap[cmd],
			"command %q appears in BOTH specific and global-only telemetry lists — pick one", cmd)
	}

	// Verify lists are sorted (for maintainability and merge conflict avoidance)
	for i := 1; i < len(commandsWithSpecificTelemetry); i++ {
		require.Less(t, commandsWithSpecificTelemetry[i-1], commandsWithSpecificTelemetry[i],
			"commandsWithSpecificTelemetry is not sorted: %q should come before %q",
			commandsWithSpecificTelemetry[i-1], commandsWithSpecificTelemetry[i])
	}
	for i := 1; i < len(commandsWithOnlyGlobalTelemetry); i++ {
		require.Less(t, commandsWithOnlyGlobalTelemetry[i-1], commandsWithOnlyGlobalTelemetry[i],
			"commandsWithOnlyGlobalTelemetry is not sorted: %q should come before %q",
			commandsWithOnlyGlobalTelemetry[i-1], commandsWithOnlyGlobalTelemetry[i])
	}

	// Verify combined coverage is non-empty and reasonable
	totalClassified := len(commandsWithSpecificTelemetry) + len(commandsWithOnlyGlobalTelemetry)
	require.Greater(t, totalClassified, 0, "no commands classified — lists are empty")

	// Verify no duplicates within each list
	seen := make(map[string]bool)
	for _, cmd := range commandsWithSpecificTelemetry {
		require.False(t, seen[cmd], "duplicate command in commandsWithSpecificTelemetry: %q", cmd)
		seen[cmd] = true
	}
	seen = make(map[string]bool)
	for _, cmd := range commandsWithOnlyGlobalTelemetry {
		require.False(t, seen[cmd], "duplicate command in commandsWithOnlyGlobalTelemetry: %q", cmd)
		seen[cmd] = true
	}
}

func Test_NewUploadAction_Constructor(t *testing.T) {
	t.Parallel()
	opts := &internal.GlobalCommandOptions{NoPrompt: true}
	a := newUploadAction(opts)
	ua := a.(*uploadAction)
	require.Same(t, opts, ua.rootOptions)
}

func Test_UploadAction_NilTelemetrySystem(t *testing.T) {
	t.Parallel()
	action := newUploadAction(&internal.GlobalCommandOptions{})
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)
}
