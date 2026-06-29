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

// Hard-coded relative locations for the on-disk Bicep tree the user owns
// after running `azd ai agent init --infra`. azure.yaml's infra.path /
// infra.module overrides are deliberately not honored; the eject writer
// hard-codes these same paths.
const (
	onDiskInfraDir       = "infra"
	onDiskBicepFile      = "main.bicep"
	onDiskBicepParamFile = "main.bicepparam"
	onDiskParamsFile     = "main.parameters.json"
)

// templateMode records which on-disk source was used, for telemetry /
// deployment tags. Embedded means the extension's pre-compiled ARM JSON.
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

// templateSource is the fully-resolved input to an ARM deployment: the
// compiled template plus the merged parameter map. sourcePath is set for
// on-disk sources so logs/errors can name the user's file.
type templateSource struct {
	mode        templateMode
	armTemplate map[string]any
	parameters  map[string]any // ARM-shape: {name: {"value": ...}}
	sourcePath  string         // absolute path; "" when embedded
}

// bicepCompiler is the seam between the on-disk loader and the bicep CLI.
// Concrete impl is *bicep.Cli from azd-core; tests inject a stub.
type bicepCompiler interface {
	Build(ctx context.Context, file string) (bicep.BuildResult, error)
	BuildBicepParam(ctx context.Context, file string, env []string) (bicep.BuildResult, error)
}

// loadOnDiskTemplate compiles the on-disk Bicep source (if any) and returns
// a fully-resolved templateSource. Returns (nil, nil) -- not an error -- when
// no on-disk template is found, so the caller falls back to the embedded path.
//
// Precedence mirrors core's BicepProvider:
//
//  1. main.bicepparam   -> bicep build-params (one-shot template + params)
//  2. main.bicep        -> bicep build + load main.parameters.json
//  3. neither           -> (nil, nil)
//
// envValues feeds both `bicep build-params` and ${VAR} substitution in
// main.parameters.json.
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

// loadFromBicep compiles bicepPath via `bicep build`, then layers in any
// user-supplied parameters from paramsPath. A missing parameters file is not
// an error; the caller's host-derived map fills the gaps via mergeParameters.
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

// loadFromBicepParam compiles bicepparamPath via `bicep build-params`, which
// emits a JSON envelope containing both the template and the resolved params.
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

	// parametersJson is itself an ARM-parameters-file shape; extract the inner
	// "parameters" map so it matches the .bicep + parameters.json path.
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

// loadParametersFile reads an ARM parameters file and substitutes ${VAR}
// references against envValues. Returns an empty map when the file is absent.
//
// ${VAR} resolution:
//   - present in envValues  -> substituted with the value
//   - missing               -> substituted with ""; if the value collapses to
//     "", the parameter is DROPPED so the template default wins
//   - ${VAR=fallback}       -> default applies; parameter is KEPT
//
// Each parameter is substituted in isolation so one unresolved VAR doesn't
// affect siblings.
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

	// Parse the "parameters" map first, then per-entry substitute the JSON
	// encoding of each value.
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

// substituteParamValue runs envsubst over the JSON encoding of one parameter
// entry. Returns nil when the entry should be dropped (string value collapsed
// to "" AND at least one referenced VAR was unset).
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
		v, ok := envValues[varName]
		if !ok {
			hasUnsetEnvVar = true
			return ""
		}
		// The value is being injected into a JSON-encoded entry, so JSON-escape
		// it. Values that are themselves valid azd values can contain quotes,
		// backslashes (Windows paths), or newlines, which would otherwise corrupt
		// the encoded string and fail the unmarshal below.
		escaped, err := json.Marshal(v)
		if err != nil {
			return v
		}
		return string(escaped[1 : len(escaped)-1])
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

	// Drop string-valued parameters whose substituted value collapsed to ""
	// because of an unresolved ${VAR}. Non-string values are always kept.
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

// mergeParameters layers host-derived parameters UNDER user-supplied ones:
// the user's value wins on keys present in both. Lets host-derived values like
// `location` and `principalId` flow through when the user's file omits them.
func mergeParameters(userParams, hostParams map[string]any) map[string]any {
	out := make(map[string]any, len(userParams)+len(hostParams))
	maps.Copy(out, hostParams)
	maps.Copy(out, userParams)
	return out
}

// unmarshalARMTemplate parses an ARM template JSON string into the untyped
// map shape armresources.DeploymentProperties.Template expects.
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

// fileExistsAt returns true when path exists and is a regular file.
func fileExistsAt(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// envValuesToKeyEquals turns a name->value map into "name=value" strings for
// the bicep CLI's environment.
func envValuesToKeyEquals(envValues map[string]string) []string {
	out := make([]string, 0, len(envValues))
	for k, v := range envValues {
		out = append(out, k+"="+v)
	}
	return out
}
