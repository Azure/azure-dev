// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// errWriter always returns an error on Write.
type errWriter struct{}

func (e *errWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write error")
}

// ──────────────────────────────────────────────────────────────
// configListAlphaAction.Run — exercises lines 475-498 (8 stmts)
// ──────────────────────────────────────────────────────────────

func Test_ConfigListAlpha_HappyPath_Finish(t *testing.T) {
	t.Parallel()
	fm := alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig())
	console := mockinput.NewMockConsole()
	action := newConfigListAlphaAction(fm, console, nil)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	_ = result
}

// ──────────────────────────────────────────────────────────────
// configOptionsAction.Run — table format with complex config values
// Exercises switch cases at lines 621-626 (map/array/default)
// ──────────────────────────────────────────────────────────────

type finishConfigMgr struct {
	cfg config.Config
	err error
}

func (m *finishConfigMgr) Load() (config.Config, error) { return m.cfg, m.err }
func (m *finishConfigMgr) Save(_ config.Config) error   { return nil }

func Test_ConfigOptions_TableFormat_MapValue_Finish(t *testing.T) {
	t.Parallel()
	// Set a known config key to a map value to hit case map[string]any
	cfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": map[string]any{"nested": "value"},
		},
	})
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.TableFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigOptions_TableFormat_ArrayValue_Finish(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": []any{"sub1", "sub2"},
		},
	})
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.TableFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigOptions_TableFormat_IntValue_Finish(t *testing.T) {
	t.Parallel()
	// Set a known config key to an integer to hit the default case
	cfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": 42,
		},
	})
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.TableFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

// ──────────────────────────────────────────────────────────────
// configOptionsAction.Run — default (none) format with complex values
// Exercises switch cases at lines 697-702 (map/array/default)
// ──────────────────────────────────────────────────────────────

func Test_ConfigOptions_DefaultFormat_MapValue_Finish(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": map[string]any{"nested": "value"},
		},
	})
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.NoneFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigOptions_DefaultFormat_ArrayValue_Finish(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": []any{"a", "b"},
		},
	})
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.NoneFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigOptions_DefaultFormat_IntValue_Finish(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": 99,
		},
	})
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.NoneFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

// ──────────────────────────────────────────────────────────────
// envGetValueAction.Run — writer error path (line 1442-1443)
// ──────────────────────────────────────────────────────────────

func Test_EnvGetValueAction_WriterError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", map[string]string{"MY_KEY": "my_val"})
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	console := mockinput.NewMockConsole()
	w := &errWriter{}

	action := newEnvGetValueAction(azdCtx, mgr, console, w, &envGetValueFlags{}, []string{"MY_KEY"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "writing key value")
}

// ──────────────────────────────────────────────────────────────
// envGetValueAction.Run — env not found path (line 1421-1427)
// ──────────────────────────────────────────────────────────────

func Test_EnvGetValueAction_EnvNotFound_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "missing"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return((*environment.Environment)(nil), environment.ErrNotFound)

	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	action := newEnvGetValueAction(azdCtx, mgr, console, buf, &envGetValueFlags{}, []string{"KEY"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

// ──────────────────────────────────────────────────────────────
// envGetValueAction.Run — generic Get error (line 1428-1429)
// ──────────────────────────────────────────────────────────────

func Test_EnvGetValueAction_GenericError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return((*environment.Environment)(nil), errors.New("storage err"))

	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	action := newEnvGetValueAction(azdCtx, mgr, console, buf, &envGetValueFlags{}, []string{"KEY"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "ensuring environment exists")
}

// ──────────────────────────────────────────────────────────────
// envGetValueAction.Run — key not found path (line 1434-1438)
// ──────────────────────────────────────────────────────────────

func Test_EnvGetValueAction_KeyNotFound_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", map[string]string{"EXISTING": "val"})
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	action := newEnvGetValueAction(azdCtx, mgr, console, buf, &envGetValueFlags{}, []string{"NONEXISTENT"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

// ──────────────────────────────────────────────────────────────
// envGetValueAction.Run — happy path with env flag override
// Exercises line 1417-1418
// ──────────────────────────────────────────────────────────────

func Test_EnvGetValueAction_EnvFlagOverride_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "default-env"}))

	env := environment.NewWithValues("other-env", map[string]string{"KEY": "value"})
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "other-env").Return(env, nil)

	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	flags := &envGetValueFlags{}
	flags.EnvironmentName = "other-env"
	action := newEnvGetValueAction(azdCtx, mgr, console, buf, flags, []string{"KEY"})
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "value")
}

// ──────────────────────────────────────────────────────────────
// envGetValuesAction.Run — env not found (line 1332-1336)
// ──────────────────────────────────────────────────────────────

func Test_EnvGetValuesAction_EnvNotFound_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "missing"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return((*environment.Environment)(nil), environment.ErrNotFound)

	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	flags := &envGetValuesFlags{}
	action := newEnvGetValuesAction(azdCtx, mgr, console, formatter, buf, flags)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

// ──────────────────────────────────────────────────────────────
// envGetValuesAction.Run — generic Get error (line 1337-1338)
// ──────────────────────────────────────────────────────────────

func Test_EnvGetValuesAction_GenericError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return((*environment.Environment)(nil), errors.New("db err"))

	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	flags := &envGetValuesFlags{}
	action := newEnvGetValuesAction(azdCtx, mgr, console, formatter, buf, flags)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "ensuring environment exists")
}

// ──────────────────────────────────────────────────────────────
// envGetValuesAction.Run — env flag override (line 1316-1317)
// ──────────────────────────────────────────────────────────────

func Test_EnvGetValuesAction_EnvFlagOverride_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "default"}))

	env := environment.NewWithValues("other", map[string]string{"A": "1"})
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "other").Return(env, nil)

	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	flags := &envGetValuesFlags{}
	flags.EnvironmentName = "other"
	action := newEnvGetValuesAction(azdCtx, mgr, console, formatter, buf, flags)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

// ──────────────────────────────────────────────────────────────
// configShowAction.Run — error on formatter.Format (line 219-221)
// Use errWriter to cause table formatter error.
// ──────────────────────────────────────────────────────────────

func Test_ConfigShowAction_FormatError_Finish(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	mgr := &finishConfigMgr{cfg: cfg}
	w := &errWriter{}
	formatter := &output.JsonFormatter{}
	action := newConfigShowAction(mgr, formatter, w)
	_, err := action.Run(t.Context())
	// JsonFormatter writing to errWriter should error
	require.Error(t, err)
}

// ──────────────────────────────────────────────────────────────
// configGetAction.Run — format error (line 296-298)
// ──────────────────────────────────────────────────────────────

func Test_ConfigGetAction_FormatError_Finish(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{"mykey": "myval"})
	mgr := &finishConfigMgr{cfg: cfg}
	w := &errWriter{}
	formatter := &output.JsonFormatter{}
	action := newConfigGetAction(mgr, formatter, w, []string{"mykey"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

// ──────────────────────────────────────────────────────────────
// configSetAction.Run — configManager.Load error (line 329-331)
// ──────────────────────────────────────────────────────────────

type finishFailLoadConfigMgr struct{}

func (m *finishFailLoadConfigMgr) Load() (config.Config, error) {
	return nil, errors.New("load error")
}
func (m *finishFailLoadConfigMgr) Save(_ config.Config) error { return nil }

func Test_ConfigSetAction_LoadError_Finish(t *testing.T) {
	t.Parallel()
	mgr := &finishFailLoadConfigMgr{}
	action := newConfigSetAction(mgr, nil)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

// ──────────────────────────────────────────────────────────────
// configUnsetAction.Run — configManager.Load error (line 360-362)
// ──────────────────────────────────────────────────────────────

func Test_ConfigUnsetAction_LoadError_Finish(t *testing.T) {
	t.Parallel()
	mgr := &finishFailLoadConfigMgr{}
	action := newConfigUnsetAction(mgr, nil)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

// ──────────────────────────────────────────────────────────────
// configOptionsAction.Run — configManager.Load non-file error (line 577-582)
// Tests the warning stderr path for non-file-not-found errors
// ──────────────────────────────────────────────────────────────

func Test_ConfigOptions_LoadWarning_Finish(t *testing.T) {
	t.Parallel()
	mgr := &finishFailLoadConfigMgr{} // returns generic error (not os.IsNotExist)
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.NoneFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err) // should still work, just log warning
}

// ──────────────────────────────────────────────────────────────
// configOptionsAction.Run — JSON format error (line 587-589)
// ──────────────────────────────────────────────────────────────

func Test_ConfigOptions_JsonFormatError_Finish(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	w := &errWriter{}
	formatter := &output.JsonFormatter{}
	action := newConfigOptionsAction(console, formatter, w, mgr, nil)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed formatting config options")
}

// ──────────────────────────────────────────────────────────────
// configOptionsAction.Run — table format error (line 676-678)
// ──────────────────────────────────────────────────────────────

func Test_ConfigOptions_TableFormatError_Finish(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	w := &errWriter{}
	formatter := &output.TableFormatter{}
	action := newConfigOptionsAction(console, formatter, w, mgr, nil)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed formatting config options")
}

// ──────────────────────────────────────────────────────────────
// envSetAction.Run — warn key case conflicts (line ~299-315)
// Setting a key with different case than existing key exercises warnKeyCaseConflicts
// ──────────────────────────────────────────────────────────────

func Test_EnvSetAction_KeyCaseConflict_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)

	// Env has "MY_KEY" set; we'll set "my_key" to trigger case conflict warning
	env := environment.NewWithValues("test", map[string]string{"MY_KEY": "old"})
	mgr := newTestEnvManager()
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	console := mockinput.NewMockConsole()

	flags := &envSetFlags{}
	action := newEnvSetAction(azdCtx, env, mgr, console, flags, []string{"my_key", "new"})
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

// ──────────────────────────────────────────────────────────────
// envConfigSetAction.Run — invalid value format (parseConfigValue deeper)
// ──────────────────────────────────────────────────────────────

func Test_EnvConfigSetAction_JsonObjectValue_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvConfigSetAction(azdCtx, mgr, &envConfigSetFlags{}, []string{"mypath", `{"key":"val"}`})
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_EnvConfigSetAction_JsonArrayValue_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvConfigSetAction(azdCtx, mgr, &envConfigSetFlags{}, []string{"mypath", `["a","b"]`})
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_EnvConfigSetAction_BoolValue_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvConfigSetAction(azdCtx, mgr, &envConfigSetFlags{}, []string{"mypath", "true"})
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_EnvConfigSetAction_IntValue_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvConfigSetAction(azdCtx, mgr, &envConfigSetFlags{}, []string{"mypath", "42"})
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

// ──────────────────────────────────────────────────────────────
// unused import guard
// ──────────────────────────────────────────────────────────────

var _ io.Writer = (*errWriter)(nil)

// ──────────────────────────────────────────────────────────────
// MORE TESTS: targeting the last ~13 stmts needed for 55%
// ──────────────────────────────────────────────────────────────

// configGetAction.Run — Load error (config.go:280-282, 2 stmts)
func Test_ConfigGetAction_LoadError_Finish(t *testing.T) {
	t.Parallel()
	mgr := &finishFailLoadConfigMgr{}
	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	action := newConfigGetAction(mgr, formatter, buf, []string{"any"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

// configSetAction.Run — Set error (config.go:329-331, 2 stmts)
// When a.b is attempted but a is a string, config.Set returns error.
func Test_ConfigSetAction_SetError_Finish(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{"a": "scalar"})
	mgr := &finishConfigMgr{cfg: cfg}
	action := newConfigSetAction(mgr, []string{"a.b", "value"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed setting configuration")
}

// configUnsetAction.Run — Unset error (config.go:360-362, 2 stmts)
func Test_ConfigUnsetAction_UnsetError_Finish(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{"a": "scalar"})
	mgr := &finishConfigMgr{cfg: cfg}
	action := newConfigUnsetAction(mgr, []string{"a.b"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed removing configuration")
}

// envConfigGetAction.Run — format error (env.go:1535-1537, 2 stmts)
func Test_EnvConfigGetAction_FormatError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", nil)
	require.NoError(t, env.Config.Set("mykey", "myval"))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	w := &errWriter{}
	formatter := &output.JsonFormatter{}
	action := newEnvConfigGetAction(azdCtx, mgr, formatter, w, &envConfigGetFlags{}, []string{"mykey"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failing formatting config values")
}

// envConfigGetAction.Run — env not found (env.go:1513-1519, 5 stmts)
func Test_EnvConfigGetAction_EnvNotFound_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "missing"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return((*environment.Environment)(nil), environment.ErrNotFound)

	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	action := newEnvConfigGetAction(azdCtx, mgr, formatter, buf, &envConfigGetFlags{}, []string{"key"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

// envConfigGetAction.Run — generic Get error (env.go:1519-1521, 2 stmts)
func Test_EnvConfigGetAction_GenericError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return((*environment.Environment)(nil), errors.New("boom"))

	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	action := newEnvConfigGetAction(azdCtx, mgr, formatter, buf, &envConfigGetFlags{}, []string{"key"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "getting environment")
}

// envConfigGetAction.Run — key not found (env.go:1526-1531, 4 stmts)
func Test_EnvConfigGetAction_KeyNotFound_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	action := newEnvConfigGetAction(azdCtx, mgr, formatter, buf, &envConfigGetFlags{}, []string{"nonexistent"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value at path")
}

// envConfigGetAction.Run — env flag override (env.go:1508-1509, 2 stmts)
func Test_EnvConfigGetAction_EnvFlagOverride_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	// no default env set — flag should override

	env := environment.NewWithValues("override-env", nil)
	require.NoError(t, env.Config.Set("thekey", "theval"))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	flags := &envConfigGetFlags{}
	flags.EnvironmentName = "override-env"
	action := newEnvConfigGetAction(azdCtx, mgr, formatter, buf, flags, []string{"thekey"})
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

// envConfigUnsetAction.Run — Save error (env.go: envManager.Save error)
func Test_EnvConfigUnsetAction_SaveError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", nil)
	require.NoError(t, env.Config.Set("mykey", "myval"))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(errors.New("save fail"))

	action := newEnvConfigUnsetAction(azdCtx, mgr, &envConfigUnsetFlags{}, []string{"mykey"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "saving environment")
}

// envConfigSetAction.Run — config.Set error (env.go:1625-1627, 2 stmts)
func Test_EnvConfigSetAction_SetError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", nil)
	// set "a" to a scalar so "a.b" will fail in Config.Set
	require.NoError(t, env.Config.Set("a", "scalar"))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	action := newEnvConfigSetAction(azdCtx, mgr, &envConfigSetFlags{}, []string{"a.b", "value"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed setting configuration")
}

// envConfigUnsetAction.Run — config.Unset error (env.go:1724-1726, 2 stmts)
func Test_EnvConfigUnsetAction_UnsetError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", nil)
	require.NoError(t, env.Config.Set("a", "scalar"))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	action := newEnvConfigUnsetAction(azdCtx, mgr, &envConfigUnsetFlags{}, []string{"a.b"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed removing configuration")
}

// envConfigSetAction.Run — Save error (env.go:1629-1631, 2 stmts)
func Test_EnvConfigSetAction_SaveError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"}))

	env := environment.NewWithValues("test", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(errors.New("save fail"))

	action := newEnvConfigSetAction(azdCtx, mgr, &envConfigSetFlags{}, []string{"mykey", "myval"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "saving environment")
}

// ──────────────────────────────────────────────────────────────
// Tests triggering GetDefaultEnvironmentName error by writing bad JSON
// Each covers 2 stmts at the error guard lines
// ──────────────────────────────────────────────────────────────

// newBadConfigAzdContext creates an azdCtx with a corrupt .azure/config.json
// so that GetDefaultEnvironmentName returns an error.
func newBadConfigAzdContext(t *testing.T) *azdcontext.AzdContext {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, azdcontext.ProjectFileName), []byte("name: test\n"), 0600))
	azDir := filepath.Join(dir, ".azure")
	require.NoError(t, os.MkdirAll(azDir, 0700))
	// Write corrupt JSON so json.Unmarshal fails
	require.NoError(t, os.WriteFile(filepath.Join(azDir, "config.json"), []byte("{bad json"), 0600))
	return azdcontext.NewAzdContextWithDirectory(dir)
}

// envGetValueAction — GetDefaultEnvironmentName error (env.go:1410-1412)
func Test_EnvGetValueAction_BadConfig_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newBadConfigAzdContext(t)
	mgr := newTestEnvManager()
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	action := newEnvGetValueAction(azdCtx, mgr, console, buf, &envGetValueFlags{}, []string{"KEY"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "deserializing config file")
}

// envConfigGetAction — GetDefaultEnvironmentName error (env.go:1505-1507)
func Test_EnvConfigGetAction_BadConfig_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newBadConfigAzdContext(t)
	mgr := newTestEnvManager()
	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	action := newEnvConfigGetAction(azdCtx, mgr, formatter, buf, &envConfigGetFlags{}, []string{"KEY"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "deserializing config file")
}

// envConfigSetAction — GetDefaultEnvironmentName error (env.go:1603-1605)
func Test_EnvConfigSetAction_BadConfig_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newBadConfigAzdContext(t)
	mgr := newTestEnvManager()
	action := newEnvConfigSetAction(azdCtx, mgr, &envConfigSetFlags{}, []string{"key", "val"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "deserializing config file")
}

// envConfigUnsetAction — GetDefaultEnvironmentName error (env.go:1703-1705)
func Test_EnvConfigUnsetAction_BadConfig_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newBadConfigAzdContext(t)
	mgr := newTestEnvManager()
	action := newEnvConfigUnsetAction(azdCtx, mgr, &envConfigUnsetFlags{}, []string{"key"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "deserializing config file")
}

// envGetValuesAction — GetDefaultEnvironmentName error (env.go:1309-1311)
func Test_EnvGetValuesAction_BadConfig_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newBadConfigAzdContext(t)
	mgr := newTestEnvManager()
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	action := newEnvGetValuesAction(azdCtx, mgr, console, formatter, buf, &envGetValuesFlags{})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "deserializing config file")
}

// envSetAction — file with bad dotenv content (env.go:240-242)
func Test_EnvSetAction_FileParseError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("test", nil)
	mgr := newTestEnvManager()

	// Write a file with invalid dotenv content
	tmpDir := t.TempDir()
	badFile := filepath.Join(tmpDir, "bad.env")
	// dotenv parser fails on lines with bare = or other malformed content; use a control char
	require.NoError(t, os.WriteFile(badFile, []byte("'unterminated\n"), 0600))
	flags := &envSetFlags{file: badFile}
	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), flags, nil)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse file")
}

// envSetAction — file that results in zero key-values (env.go:266-272)
func Test_EnvSetAction_EmptyFile_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("test", nil)
	mgr := newTestEnvManager()

	tmpDir := t.TempDir()
	emptyFile := filepath.Join(tmpDir, "empty.env")
	require.NoError(t, os.WriteFile(emptyFile, []byte("\n\n# comment only\n\n"), 0600))
	flags := &envSetFlags{file: emptyFile}
	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), flags, nil)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no environment values")
}

// envSelectAction — console.Select error (env.go:815-817)
func Test_EnvSelectAction_SelectError_Finish(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return(
		[]*environment.Description{{Name: "env1"}, {Name: "env2"}},
		nil,
	)

	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(_ input.ConsoleOptions) (any, error) {
		return 0, errors.New("select cancelled")
	})

	action := newEnvSelectAction(azdCtx, mgr, console, nil) // nil args → prompts
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "selecting environment")
}

// envSelectAction — SetProjectState error (env.go:836-838)
func Test_EnvSelectAction_SetProjectStateError_Finish(t *testing.T) {
	t.Parallel()
	// Use a directory where .azure is a FILE instead of a directory,
	// so writing .azure/config.json fails.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, azdcontext.ProjectFileName), []byte("name: test\n"), 0600))
	// Create .azure as a regular file — SetProjectState will fail trying to write .azure/config.json
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".azure"), []byte("blocker"), 0600))
	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)

	env := environment.NewWithValues("env1", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	console := mockinput.NewMockConsole()
	action := newEnvSelectAction(azdCtx, mgr, console, []string{"env1"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "setting default environment")
}
