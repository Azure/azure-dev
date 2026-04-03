// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

// mockConfig implements config.Config for testing.
type mockConfig struct {
	data    map[string]any
	unsetFn func(path string) error
}

func (m *mockConfig) Get(path string) (any, bool) {
	v, ok := m.data[path]
	return v, ok
}

func (m *mockConfig) GetString(path string) (string, bool) {
	v, ok := m.data[path]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func (m *mockConfig) GetSection(path string, section any) (bool, error) {
	return false, nil
}

func (m *mockConfig) GetMap(path string) (map[string]any, bool) {
	v, ok := m.data[path]
	if !ok {
		return nil, false
	}
	mp, ok := v.(map[string]any)
	return mp, ok
}

func (m *mockConfig) GetSlice(path string) ([]any, bool) {
	v, ok := m.data[path]
	if !ok {
		return nil, false
	}
	sl, ok := v.([]any)
	return sl, ok
}

func (m *mockConfig) Set(path string, value any) error {
	m.data[path] = value
	return nil
}

func (m *mockConfig) SetSecret(path string, value string) error {
	m.data[path] = value
	return nil
}

func (m *mockConfig) Unset(path string) error {
	if m.unsetFn != nil {
		return m.unsetFn(path)
	}
	delete(m.data, path)
	return nil
}

func (m *mockConfig) IsEmpty() bool {
	return len(m.data) == 0
}

func (m *mockConfig) Raw() map[string]any {
	return m.data
}

func (m *mockConfig) ResolvedRaw() map[string]any {
	return m.data
}

// mockUserConfigManager implements config.UserConfigManager for testing.
type mockUserConfigManager struct {
	config.UserConfigManager
	cfg    config.Config
	saveFn func(config.Config) error
}

func (m *mockUserConfigManager) Load() (config.Config, error) {
	return m.cfg, nil
}

func (m *mockUserConfigManager) Save(c config.Config) error {
	if m.saveFn != nil {
		return m.saveFn(c)
	}
	return nil
}

func TestNewUserConfigService(t *testing.T) {
	t.Parallel()
	mgr := &mockUserConfigManager{cfg: &mockConfig{data: map[string]any{}}}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)
	require.NotNil(t, svc)
}

func TestUserConfigService_Get_Found(t *testing.T) {
	t.Parallel()
	mgr := &mockUserConfigManager{cfg: &mockConfig{data: map[string]any{"test.key": "value"}}}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	resp, err := svc.Get(t.Context(), &azdext.GetUserConfigRequest{Path: "test.key"})
	require.NoError(t, err)
	require.True(t, resp.Found)
	require.Contains(t, string(resp.Value), "value")
}

func TestUserConfigService_Get_NotFound(t *testing.T) {
	t.Parallel()
	mgr := &mockUserConfigManager{cfg: &mockConfig{data: map[string]any{}}}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	resp, err := svc.Get(t.Context(), &azdext.GetUserConfigRequest{Path: "missing"})
	require.NoError(t, err)
	require.False(t, resp.Found)
}

func TestUserConfigService_GetString_Found(t *testing.T) {
	t.Parallel()
	mgr := &mockUserConfigManager{cfg: &mockConfig{data: map[string]any{"k": "v"}}}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	resp, err := svc.GetString(t.Context(), &azdext.GetUserConfigStringRequest{Path: "k"})
	require.NoError(t, err)
	require.True(t, resp.Found)
	require.Equal(t, "v", resp.Value)
}

func TestUserConfigService_GetString_NotFound(t *testing.T) {
	t.Parallel()
	mgr := &mockUserConfigManager{cfg: &mockConfig{data: map[string]any{}}}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	resp, err := svc.GetString(t.Context(), &azdext.GetUserConfigStringRequest{Path: "missing"})
	require.NoError(t, err)
	require.False(t, resp.Found)
}

func TestUserConfigService_Set_Success(t *testing.T) {
	t.Parallel()
	cfg := &mockConfig{data: map[string]any{}}
	mgr := &mockUserConfigManager{cfg: cfg}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	resp, err := svc.Set(t.Context(), &azdext.SetUserConfigRequest{Path: "key", Value: []byte(`"value"`)})
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestUserConfigService_Set_SaveError(t *testing.T) {
	t.Parallel()
	cfg := &mockConfig{data: map[string]any{}}
	mgr := &mockUserConfigManager{
		cfg:    cfg,
		saveFn: func(c config.Config) error { return errors.New("save failed") },
	}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	_, err = svc.Set(t.Context(), &azdext.SetUserConfigRequest{Path: "key", Value: []byte(`"value"`)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "save failed")
}

func TestUserConfigService_Unset_Success(t *testing.T) {
	t.Parallel()
	cfg := &mockConfig{data: map[string]any{"key": "value"}}
	mgr := &mockUserConfigManager{cfg: cfg}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	resp, err := svc.Unset(t.Context(), &azdext.UnsetUserConfigRequest{Path: "key"})
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestUserConfigService_Unset_UnsetError(t *testing.T) {
	t.Parallel()
	cfg := &mockConfig{
		data:    map[string]any{},
		unsetFn: func(path string) error { return errors.New("unset failed") },
	}
	mgr := &mockUserConfigManager{cfg: cfg}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	_, err = svc.Unset(t.Context(), &azdext.UnsetUserConfigRequest{Path: "key"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unset failed")
}

func TestUserConfigService_Unset_SaveError(t *testing.T) {
	t.Parallel()
	cfg := &mockConfig{data: map[string]any{}}
	mgr := &mockUserConfigManager{
		cfg:    cfg,
		saveFn: func(c config.Config) error { return errors.New("save failed") },
	}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	_, err = svc.Unset(t.Context(), &azdext.UnsetUserConfigRequest{Path: "key"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "save failed")
}

type mockUserConfigManagerLoadError struct {
	config.UserConfigManager
}

func (m *mockUserConfigManagerLoadError) Load() (config.Config, error) {
	return nil, errors.New("load failed")
}

func TestNewUserConfigService_LoadError(t *testing.T) {
	t.Parallel()
	_, err := NewUserConfigService(&mockUserConfigManagerLoadError{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "load failed")
}

func TestUserConfigService_Set_InvalidJSON(t *testing.T) {
	t.Parallel()
	cfg := &mockConfig{data: map[string]any{}}
	mgr := &mockUserConfigManager{cfg: cfg}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	_, err = svc.Set(t.Context(), &azdext.SetUserConfigRequest{Path: "key", Value: []byte(`{invalid`)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal")
}

func TestUserConfigService_GetSection_NotFound(t *testing.T) {
	t.Parallel()
	cfg := &mockConfig{data: map[string]any{}}
	mgr := &mockUserConfigManager{cfg: cfg}
	svc, err := NewUserConfigService(mgr)
	require.NoError(t, err)

	resp, err := svc.GetSection(t.Context(), &azdext.GetUserConfigSectionRequest{Path: "missing.section"})
	require.NoError(t, err)
	require.False(t, resp.Found)
}
