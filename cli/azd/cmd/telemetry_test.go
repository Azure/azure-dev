// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
// via raw attribute.String(key, ...) / attribute.Bool(key, ...) — or the chained
// attribute.Key(key).String(...) form — whether the key is a string literal or a
// named constant, and whether the package is imported under its default name or
// an alias. Every telemetry attribute must be
// declared as a fields.AttributeKey (with a Classification and Purpose) and
// emitted through it, e.g. fields.SomeKey.String(value). This keeps the telemetry
// schema discoverable and classifiable for the GDPR metadata pipeline (azure-dev
// issue #1803).
//
// Legitimately excluded from the scan:
//   - *_test.go files (test fixtures build raw attributes on purpose).
//   - extensions/... — independent extension modules with their own schema.
func TestNoRawTelemetryAttributes(t *testing.T) {
	t.Parallel()

	// The test runs with its package directory (cli/azd/cmd) as the working
	// directory, so the module root is one level up.
	azdRoot, err := filepath.Abs("..")
	require.NoError(t, err)

	var violations []string

	err = filepath.Walk(azdRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			switch filepath.Base(path) {
			case "vendor", "extensions", "testdata", "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, relErr := filepath.Rel(azdRoot, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil // skip unparseable files
		}

		violations = append(violations, scanGoFileForRawAttributes(fset, file, rel)...)
		return nil
	})
	require.NoError(t, err)

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

// scanGoFileForRawAttributes returns the raw-telemetry-attribute violations in a
// single parsed Go file. rel is the display path used in messages. It is shared
// by TestNoRawTelemetryAttributes (which walks the module tree) and the fixture
// test TestRawTelemetryAttributeScanner, so the guard's contract is itself tested.
func scanGoFileForRawAttributes(fset *token.FileSet, file *ast.File, rel string) []string {
	// rawAttributeConstructors are the go.opentelemetry.io/otel/attribute helpers
	// (and the identically named attribute.Key methods) that build a KeyValue from
	// a key and value. Using them directly in product code bypasses the
	// fields.AttributeKey registry, so the GDPR metadata exporter (which discovers
	// only exported AttributeKey vars) can never classify the resulting property.
	// See docs/specs/metrics-audit/telemetry-schema.md.
	rawAttributeConstructors := map[string]struct{}{
		"String":       {},
		"Bool":         {},
		"Int":          {},
		"IntSlice":     {},
		"Int64":        {},
		"Float64":      {},
		"Stringer":     {},
		"StringSlice":  {},
		"BoolSlice":    {},
		"Int64Slice":   {},
		"Float64Slice": {},
	}

	// Resolve the local name bound to go.opentelemetry.io/otel/attribute in this
	// file. Matching the selector base literally against "attribute" would miss an
	// aliased import (e.g. otelattr "...otel/attribute") and could also misfire on
	// an unrelated local identifier named "attribute". If the file does not import
	// the package, it cannot construct a raw attribute, so there is nothing to scan.
	attrPkgName := ""
	for _, imp := range file.Imports {
		importPath, uErr := strconv.Unquote(imp.Path.Value)
		if uErr != nil || importPath != "go.opentelemetry.io/otel/attribute" {
			continue
		}
		if imp.Name != nil {
			attrPkgName = imp.Name.Name // explicit alias
		} else {
			attrPkgName = "attribute" // default package name
		}
		break
	}
	// A blank ("_") or dot (".") import cannot produce a "pkg.Constructor"
	// selector, so there is nothing this AST check can match on.
	if attrPkgName == "" || attrPkgName == "_" || attrPkgName == "." {
		return nil
	}

	// literalKey renders the first string-literal argument of a call for a
	// friendlier message, or "..." for a non-literal (e.g. const) key.
	literalKey := func(c *ast.CallExpr) string {
		if len(c.Args) == 0 {
			return "..."
		}
		if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			return lit.Value
		}
		return "..."
	}

	var violations []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Only the KeyValue-producing constructor / key-method names are of
		// interest (String, Bool, Int, ... — see rawAttributeConstructors).
		if _, isConstructor := rawAttributeConstructors[sel.Sel.Name]; !isConstructor {
			return true
		}

		pos := fset.Position(call.Pos())

		// Form A: attribute.String("key", v) / attribute.Bool(k, v) — the selector
		// base is the imported package identifier. This bypasses the
		// fields.AttributeKey registry, so the GDPR classifier can never see it,
		// whether the key is a string literal or a named constant.
		if base, ok := sel.X.(*ast.Ident); ok && base.Name == attrPkgName {
			violations = append(violations, fmt.Sprintf(
				"  %s:%d: %s.%s(%s, ...)", rel, pos.Line, attrPkgName, sel.Sel.Name, literalKey(call)))
			return true
		}

		// Form B: attribute.Key("key").String(v) — the selector base is an inline
		// attribute.Key(...) constructor call. This produces a KeyValue that
		// likewise bypasses the fields.AttributeKey registry. (A method call on a
		// Key-typed *variable* is indistinguishable at the AST level from the
		// sanctioned fields.SomeKey.String(v) promoted-method call, so only the
		// inline form is enforceable here.)
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			if innerSel, ok := inner.Fun.(*ast.SelectorExpr); ok {
				if innerBase, ok := innerSel.X.(*ast.Ident); ok &&
					innerBase.Name == attrPkgName && innerSel.Sel.Name == "Key" {
					violations = append(violations, fmt.Sprintf(
						"  %s:%d: %s.Key(%s).%s(...)", rel, pos.Line, attrPkgName, literalKey(inner), sel.Sel.Name))
					return true
				}
			}
		}

		return true
	})

	return violations
}

// TestRawTelemetryAttributeScanner is a fixture test for the AST guard used by
// TestNoRawTelemetryAttributes. It pins the contract: raw attribute constructors,
// aliased imports, constant keys, and the chained attribute.Key(k).String(v) form
// are all flagged, while the sanctioned promoted-method pattern and a bare
// attribute.Key(k) (which does not build a KeyValue) are not.
func TestRawTelemetryAttributeScanner(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		src           string
		wantViolation bool
	}{
		{
			name:          "literal key",
			src:           `package p; import "go.opentelemetry.io/otel/attribute"; var _ = attribute.String("raw.key", "v")`,
			wantViolation: true,
		},
		{
			name:          "constant key",
			src:           `package p; import "go.opentelemetry.io/otel/attribute"; const k = "raw.key"; var _ = attribute.String(k, "v")`,
			wantViolation: true,
		},
		{
			name:          "aliased import",
			src:           `package p; import otelattr "go.opentelemetry.io/otel/attribute"; var _ = otelattr.Bool("raw.key", true)`,
			wantViolation: true,
		},
		{
			name:          "int slice constructor",
			src:           `package p; import "go.opentelemetry.io/otel/attribute"; var _ = attribute.IntSlice("raw.key", []int{1})`,
			wantViolation: true,
		},
		{
			name:          "chained key method",
			src:           `package p; import "go.opentelemetry.io/otel/attribute"; var _ = attribute.Key("raw.key").String("v")`,
			wantViolation: true,
		},
		{
			name:          "bare key builder without value",
			src:           `package p; import "go.opentelemetry.io/otel/attribute"; var _ = attribute.Key("raw.key")`,
			wantViolation: false,
		},
		{
			name:          "promoted method on key-typed value",
			src:           `package p; import "go.opentelemetry.io/otel/attribute"; func f(k attribute.Key) { _ = k.String("v") }`,
			wantViolation: false,
		},
		{
			name:          "file without the attribute import",
			src:           `package p; var _ = 1`,
			wantViolation: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, tc.name+".go", tc.src, 0)
			require.NoError(t, err)

			got := scanGoFileForRawAttributes(fset, file, tc.name+".go")
			if tc.wantViolation {
				require.NotEmpty(t, got, "expected a violation for %q", tc.name)
			} else {
				require.Empty(t, got, "expected no violation for %q, got %v", tc.name, got)
			}
		})
	}
}
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
