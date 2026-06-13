// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/drone/envsubst"
)

// Hard-coded relative locations for the on-disk Bicep tree the user
// owns after running `azd ai agent init --infra`. Mirrors the spec's
// example output (spec/bicepless-foundry/spec.md §Eject Command) and
// the eject writer in `internal/cmd/init_infra.go`. We deliberately do
// NOT honor `azure.yaml`'s `infra.path` / `infra.module` overrides
// for now -- the spec doesn't ask for them and the eject writer hard-
// codes the same paths. Easy follow-up if a real use case appears.
const (
	onDiskInfraDir       = "infra"
	onDiskBicepFile      = "main.bicep"
	onDiskBicepParamFile = "main.bicepparam"
	onDiskParamsFile     = "main.parameters.json"
)

// templateMode records which on-disk source was used so the caller can
// surface it in telemetry / deployment tags. Embedded means "fall back
// to the extension's pre-compiled ARM JSON".
type templateMode int

const (
	templateModeEmbedded   templateMode = iota // no on-disk template found
	templateModeBicep                          // ./infra/main.bicep
	templateModeBicepParam                     // ./infra/main.bicepparam
)

func (m templateMode) String() string {
	switch m {
	case templateModeBicep:
		return "ondisk_bicep"
	case templateModeBicepParam:
		return "ondisk_bicepparam"
	default:
		return "embedded"
	}
}

// templateSource is the fully-resolved input to an ARM deployment:
// the compiled ARM template plus the merged parameter map ready to
// hand to `armresources.DeploymentProperties`. `sourcePath` is
// populated for on-disk sources so logs/errors can name the file the
// user actually owns.
type templateSource struct {
	mode        templateMode
	armTemplate map[string]any
	parameters  map[string]any // ARM-shape: {name: {"value": ...}}
	sourcePath  string         // absolute path; "" when embedded
}

// bicepCompiler is the seam between the on-disk loader and the bicep
// CLI. Concrete impl is *bicep.Cli from azd-core; tests inject a stub.
// We keep the two methods we actually call, not a wider surface, so
// stubs stay small.
type bicepCompiler interface {
	Build(ctx context.Context, file string) (bicep.BuildResult, error)
	BuildBicepParam(ctx context.Context, file string, env []string) (bicep.BuildResult, error)
}

// loadOnDiskTemplate inspects projectPath/infra/ and, if a Bicep
// source is present, compiles it via the supplied compiler and
// returns a fully-resolved templateSource. Returns (nil, nil) -- NOT
// an error -- when no on-disk template is found; the caller is
// expected to fall back to the embedded path in that case.
//
// Precedence mirrors core's BicepProvider.Initialize
// (cli/azd/pkg/infra/provisioning/bicep/bicep_provider.go:297-307):
//
//  1. main.bicepparam   -> bicep build-params (one-shot template + params)
//  2. main.bicep        -> bicep build + load main.parameters.json
//  3. neither           -> (nil, nil)
//
// envValues feeds three things:
//   - the env passed to `bicep build-params` (so its
//     readEnvironmentVariable() calls resolve)
//   - ${VAR} substitution inside main.parameters.json
func loadOnDiskTemplate(
	ctx context.Context,
	projectPath string,
	compiler bicepCompiler,
	envValues map[string]string,
) (*templateSource, error) {
	infraDir := filepath.Join(projectPath, onDiskInfraDir)
	bicepparamPath := filepath.Join(infraDir, onDiskBicepParamFile)
	bicepPath := filepath.Join(infraDir, onDiskBicepFile)

	switch {
	case fileExistsAt(bicepparamPath):
		return loadFromBicepParam(ctx, bicepparamPath, compiler, envValues)
	case fileExistsAt(bicepPath):
		paramsPath := filepath.Join(infraDir, onDiskParamsFile)
		return loadFromBicep(ctx, bicepPath, paramsPath, compiler, envValues)
	default:
		return nil, nil
	}
}

// loadFromBicep compiles bicepPath via `bicep build`, then layers in
// any user-supplied parameters from paramsPath (the *.parameters.json
// file). Missing parameters file is not an error; the caller's
// host-derived map will fill in the gaps via mergeParameters.
func loadFromBicep(
	ctx context.Context,
	bicepPath, paramsPath string,
	compiler bicepCompiler,
	envValues map[string]string,
) (*templateSource, error) {
	res, err := compiler.Build(ctx, bicepPath)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeOnDiskBicepCompileFailed,
			fmt.Sprintf("compile %s: %s", bicepPath, err),
			"fix the bicep errors above and re-run `azd provision`",
		)
	}

	tmpl, err := unmarshalARMTemplate(res.Compiled, bicepPath)
	if err != nil {
		return nil, err
	}

	params, err := loadParametersFile(paramsPath, envValues)
	if err != nil {
		return nil, err
	}

	return &templateSource{
		mode:        templateModeBicep,
		armTemplate: tmpl,
		parameters:  params,
		sourcePath:  bicepPath,
	}, nil
}

// loadFromBicepParam compiles bicepparamPath via `bicep build-params`,
// which emits a JSON envelope containing both the template and
// the resolved parameters. azd-core's BicepProvider does the same
// (cli/azd/pkg/infra/provisioning/bicep/bicep_provider.go:2388-2406).
func loadFromBicepParam(
	ctx context.Context,
	bicepparamPath string,
	compiler bicepCompiler,
	envValues map[string]string,
) (*templateSource, error) {
	env := envValuesToKeyEquals(envValues)
	res, err := compiler.BuildBicepParam(ctx, bicepparamPath, env)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeOnDiskBicepCompileFailed,
			fmt.Sprintf("compile %s: %s", bicepparamPath, err),
			"fix the bicep errors above and re-run `azd provision`",
		)
	}

	// `bicep build-params --stdout` returns {"templateJson": "...", "parametersJson": "..."}.
	var envelope struct {
		TemplateJson   string `json:"templateJson"`
		ParametersJson string `json:"parametersJson"`
	}
	if err := json.Unmarshal([]byte(res.Compiled), &envelope); err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeOnDiskBicepParseFailed,
			fmt.Sprintf("parse bicepparam envelope from %s: %s", bicepparamPath, err),
		)
	}

	tmpl, err := unmarshalARMTemplate(envelope.TemplateJson, bicepparamPath)
	if err != nil {
		return nil, err
	}

	// The parametersJson is itself an ARM-parameters-file shape:
	// {"$schema": "...", "contentVersion": "...", "parameters": {...}}.
	// Extract the inner "parameters" map so it matches what the
	// .bicep + parameters.json path returns.
	params, err := extractParametersFromARMFile([]byte(envelope.ParametersJson), bicepparamPath)
	if err != nil {
		return nil, err
	}

	return &templateSource{
		mode:        templateModeBicepParam,
		armTemplate: tmpl,
		parameters:  params,
		sourcePath:  bicepparamPath,
	}, nil
}

// loadParametersFile reads an ARM parameters file (the standard
// `main.parameters.json` shape) and substitutes ${VAR} references
// against envValues. Returns an empty map when the file is absent
// (matches core's `loadParameters` at
// cli/azd/pkg/infra/provisioning/bicep/bicep_provider.go:2185-2195).
//
// ${VAR} resolution mirrors core's evalParamEnvSubst pattern at
// bicep_provider.go:2113-2153:
//   - present in envValues  -> substituted with the value
//   - missing               -> substituted with "" AND, if the
//     resolved value collapses to "",
//     the parameter is DROPPED so the
//     template's default (if any) wins.
//   - ${VAR=fallback}       -> envsubst supplies the default. Since
//     the resolved value is non-empty,
//     the parameter is KEPT.
//
// Each parameter is substituted in isolation (per-key) so a single
// unresolved VAR in one entry doesn't affect siblings.
func loadParametersFile(paramFilePath string, envValues map[string]string) (map[string]any, error) {
	//nolint:gosec // paramFilePath is derived from projectPath supplied by azd-core
	raw, err := os.ReadFile(paramFilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, exterrors.Internal(
			exterrors.CodeOnDiskParametersInvalid,
			fmt.Sprintf("read %s: %s", paramFilePath, err),
		)
	}

	// Parse the file's "parameters" map first (no substitution yet),
	// then per-entry substitute the JSON encoding of each value.
	// Mirrors core's bicep_provider.go:2287-2340.
	pre, err := extractParametersFromARMFile(raw, paramFilePath)
	if err != nil {
		return nil, err
	}

	out := make(map[string]any, len(pre))
	for name, raw := range pre {
		kept, err := substituteParamValue(raw, paramFilePath, name, envValues)
		if err != nil {
			return nil, err
		}
		if kept == nil {
			continue // dropped: VAR unset AND substituted value is empty
		}
		out[name] = kept
	}
	return out, nil
}

// substituteParamValue runs envsubst over the JSON encoding of one
// parameter entry. Returns nil when the entry should be dropped (string
// value collapsed to "" AND at least one referenced VAR was unset --
// matching core at bicep_provider.go:2337-2339). Otherwise returns the
// re-parsed entry with ${VAR} refs replaced.
func substituteParamValue(
	rawEntry any,
	sourcePath, name string,
	envValues map[string]string,
) (any, error) {
	enc, err := json.Marshal(rawEntry)
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeOnDiskParametersInvalid,
			fmt.Sprintf("re-encode parameter %q in %s: %s", name, sourcePath, err),
		)
	}

	hasUnsetEnvVar := false
	substituted, err := envsubst.Eval(string(enc), func(varName string) string {
		if v, ok := envValues[varName]; ok {
			return v
		}
		hasUnsetEnvVar = true
		return ""
	})
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeOnDiskParametersInvalid,
			fmt.Sprintf("substitute env vars in parameter %q of %s: %s", name, sourcePath, err),
			"check for malformed ${VAR} references in the parameters file",
		)
	}

	var resolved any
	if err := json.Unmarshal([]byte(substituted), &resolved); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeOnDiskParametersInvalid,
			fmt.Sprintf("parse parameter %q in %s after substitution: %s", name, sourcePath, err),
			"ensure the substituted value is valid JSON",
		)
	}

	// Drop string-valued parameters whose substituted value collapsed
	// to "" because of an unresolved ${VAR}. Non-string values (objects,
	// arrays, bools, numbers) are kept regardless.
	if entry, ok := resolved.(map[string]any); ok {
		if val, ok := entry["value"]; ok {
			if str, ok := val.(string); ok && str == "" && hasUnsetEnvVar {
				return nil, nil
			}
		}
	}
	return resolved, nil
}

// extractParametersFromARMFile pulls the inner "parameters" map out of
// an ARM parameters file. Returns a fresh map even when the input is
// empty so the caller can range over it without nil checks.
func extractParametersFromARMFile(raw []byte, sourcePath string) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var doc struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeOnDiskParametersInvalid,
			fmt.Sprintf("parse %s as ARM parameters JSON: %s", sourcePath, err),
			"verify the file matches the ARM deploymentParameters schema",
		)
	}
	if doc.Parameters == nil {
		return map[string]any{}, nil
	}
	return doc.Parameters, nil
}

// mergeParameters layers host-derived parameter values UNDER the
// user-supplied ones: for keys present in both, the user's value wins;
// for keys present only in one, that one wins. Used so azd-host-derived
// values like `location` and `principalId` still flow through when the
// user's parameters file doesn't declare them.
func mergeParameters(userParams, hostParams map[string]any) map[string]any {
	out := make(map[string]any, len(userParams)+len(hostParams))
	maps.Copy(out, hostParams)
	maps.Copy(out, userParams)
	return out
}

// unmarshalARMTemplate parses an ARM template JSON string into the
// shape `armresources.DeploymentProperties.Template` expects (an
// untyped map). All error paths produce a structured Internal error
// so the deploy pipeline can surface it consistently.
func unmarshalARMTemplate(raw, sourcePath string) (map[string]any, error) {
	if raw == "" {
		return nil, exterrors.Internal(
			exterrors.CodeOnDiskBicepParseFailed,
			fmt.Sprintf("compiled ARM JSON from %s is empty", sourcePath),
		)
	}
	var tmpl map[string]any
	if err := json.Unmarshal([]byte(raw), &tmpl); err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeOnDiskBicepParseFailed,
			fmt.Sprintf("parse compiled ARM JSON from %s: %s", sourcePath, err),
		)
	}
	return tmpl, nil
}

// fileExistsAt is a small helper that returns true when the path
// exists AND points to a regular file (not a directory). We avoid
// re-using the cmd package's fileExists() to keep this package
// self-contained.
func fileExistsAt(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// envValuesToKeyEquals turns a name->value map into the "name=value"
// strings the bicep CLI expects (the format Go's os/exec uses for
// environment variables). Order is undefined; the bicep CLI doesn't
// care about ordering.
func envValuesToKeyEquals(envValues map[string]string) []string {
	out := make([]string, 0, len(envValues))
	for k, v := range envValues {
		out = append(out, k+"="+v)
	}
	return out
}
