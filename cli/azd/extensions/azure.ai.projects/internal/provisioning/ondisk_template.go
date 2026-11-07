// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
)

// Default locations for the on-disk Bicep tree. A provisioning layer can
// override both through its path and module options.
const (
	onDiskInfraDir       = "infra"
	onDiskModule         = "main"
	onDiskBicepFile      = onDiskModule + ".bicep"
	onDiskBicepParamFile = onDiskModule + ".bicepparam"
	onDiskParamsFile     = onDiskModule + ".parameters.json"
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
	return loadOnDiskTemplateAt(
		ctx,
		filepath.Join(projectPath, onDiskInfraDir),
		onDiskModule,
		compiler,
		envValues,
	)
}

// loadOnDiskTemplateAt loads a Bicep module from an explicit provisioning
// layer path and module name.
func loadOnDiskTemplateAt(
	ctx context.Context,
	infraDir string,
	module string,
	compiler bicepCompiler,
	envValues map[string]string,
) (*templateSource, error) {
	if module == "" {
		module = onDiskModule
	}
	bicepparamPath := filepath.Join(infraDir, module+".bicepparam")
	bicepPath := filepath.Join(infraDir, module+".bicep")

	switch {
	case fileExistsAt(bicepparamPath):
		return loadFromBicepParam(
			ctx,
			bicepparamPath,
			compiler,
			envValues,
		)
	case fileExistsAt(bicepPath):
		paramsPath := filepath.Join(infraDir, module+".parameters.json")
		return loadFromBicep(
			ctx,
			bicepPath,
			paramsPath,
			compiler,
			envValues,
		)
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
		if _, structured := errors.AsType[*azdext.LocalError](err); structured {
			return nil, err
		}
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

	params, err := loadParametersFile(
		paramsPath,
		envValues,
	)
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
	tmpl, params, err := synthesis.CompileBicepParameters(ctx, compiler, bicepparamPath, envValues)
	if err != nil {
		return nil, err
	}
	if err := validateProjectTemplate(tmpl, bicepparamPath); err != nil {
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
	return synthesis.LoadARMParametersFile(paramFilePath, envValues)
}

// mergeParameters layers host-derived parameters UNDER user-supplied ones:
// the user's value wins on case-insensitively matching names. Lets host-derived
// values like `location` flow through when the user's file omits them.
func mergeParameters(userParams, hostParams map[string]any) map[string]any {
	out := make(map[string]any, len(userParams)+len(hostParams))
	maps.Copy(out, hostParams)
	for name := range hostParams {
		for supplied := range userParams {
			if strings.EqualFold(name, supplied) {
				delete(out, name)
				break
			}
		}
	}
	maps.Copy(out, userParams)
	return out
}

// parametersDeclaredByTemplate keeps host-derived values only when the
// compiled on-disk template declares the matching parameter. User-authored
// parameters are deliberately not filtered so ARM still reports misspellings.
func parametersDeclaredByTemplate(hostParams, armTemplate map[string]any) map[string]any {
	declared, _ := armTemplate["parameters"].(map[string]any)
	out := make(map[string]any, min(len(hostParams), len(declared)))
	for name, value := range hostParams {
		for declaration := range declared {
			if strings.EqualFold(name, declaration) {
				out[name] = value
				break
			}
		}
	}
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
	if err := validateProjectTemplate(tmpl, sourcePath); err != nil {
		return nil, err
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
