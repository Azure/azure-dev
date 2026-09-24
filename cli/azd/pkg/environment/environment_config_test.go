// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentConfigDetachedValues(t *testing.T) {
	tests := []struct {
		name string
		read func(*testing.T, config.Config) any
	}{
		{"raw", func(_ *testing.T, c config.Config) any { return c.Raw()["nested"] }},
		{"resolved raw", func(_ *testing.T, c config.Config) any { return c.ResolvedRaw()["nested"] }},
		{"get", func(t *testing.T, c config.Config) any {
			value, ok := c.Get("nested")
			require.True(t, ok)
			return value
		}},
		{"get map", func(t *testing.T, c config.Config) any {
			value, ok := c.GetMap("nested")
			require.True(t, ok)
			return value
		}},
		{"get slice", func(t *testing.T, c config.Config) any {
			value, ok := c.GetSlice("nested.list")
			require.True(t, ok)
			return map[string]any{"list": value}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := New("test")
			view := env.Config()
			input := map[string]any{"list": []any{map[string][]int{"values": {1, 2}}}}
			expected, err := config.CloneValue(input)
			require.NoError(t, err)
			require.NoError(t, view.Set("nested", input))
			require.IsType(t, []any{}, input["list"])
			input["list"].([]any)[0].(map[string][]int)["values"][0] = 3

			value := tt.read(t, view)
			require.Equal(t, expected, value)
			require.IsType(t, map[string]any{}, value)
			nested := value.(map[string]any)
			require.IsType(t, []any{}, nested["list"])
			list := nested["list"].([]any)
			require.IsType(t, map[string][]int{}, list[0])
			list[0].(map[string][]int)["values"][0] = 9
			require.Equal(t, expected, tt.read(t, view))
		})
	}
}

func TestEnvironmentConfigTypedValues(t *testing.T) {
	env := New("test")
	input := []map[string]string{{"key": "original"}}
	require.NoError(t, env.Config().Set("typed", input))
	input[0]["key"] = "input changed"
	value, exists := env.Config().Get("typed")
	require.True(t, exists)
	require.Equal(t, []map[string]string{{"key": "original"}}, value)
	require.IsType(t, input, value)
	value.([]map[string]string)[0]["key"] = "output changed"
	value, exists = env.Config().Get("typed")
	require.True(t, exists)
	require.Equal(t, []map[string]string{{"key": "original"}}, value)

	_, exists = env.Config().GetSlice("typed")
	require.False(t, exists)
	_, exists = env.Config().GetMap("typed")
	require.False(t, exists)
	_, exists = env.Config().GetString("typed")
	require.False(t, exists)
}

func TestEnvironmentConfigRetainedView(t *testing.T) {
	env := New("test")
	view := env.Config()
	require.True(t, view.IsEmpty())
	require.NoError(t, view.Set("old", true))

	require.NoError(t, env.ReplaceState(EnvironmentState{
		Config: config.NewConfig(map[string]any{"current": "value"}),
	}))

	_, exists := view.Get("old")
	require.False(t, exists)
	value, exists := view.GetString("current")
	require.True(t, exists)
	require.Equal(t, "value", value)
	require.NoError(t, view.Set("new", true))
	valueAny, exists := env.Config().Get("new")
	require.True(t, exists)
	require.Equal(t, true, valueAny)
	require.NoError(t, view.Unset("current"))
	_, exists = env.Config().Get("current")
	require.False(t, exists)
}

func TestEnvironmentConfigErrorsAndSections(t *testing.T) {
	view := New("test").Config()
	require.NoError(t, view.Set("scalar", 1))
	require.Error(t, view.Set("scalar.child", "value"))
	require.Error(t, view.Unset("scalar.child"))
	var section map[string]string
	exists, err := view.GetSection("missing", &section)
	require.False(t, exists)
	require.NoError(t, err)
	exists, err = view.GetSection("scalar", &section)
	require.True(t, exists)
	require.Error(t, err)
	require.NoError(t, view.Set("section", map[string]string{"key": "original"}))
	exists, err = view.GetSection("section", &section)
	require.True(t, exists)
	require.NoError(t, err)
	section["key"] = "changed"
	value, exists := view.Get("section")
	require.True(t, exists)
	require.Equal(t, map[string]string{"key": "original"}, value)
}

func TestEnvironmentConfigClonePreservesSecrets(t *testing.T) {
	view := New("test").Config()
	require.NoError(t, view.SetSecret("password", "unsaved-secret"))
	cloned, err := config.Clone(view)
	require.NoError(t, err)
	require.Equal(t, view.Raw(), cloned.Raw())
	password, exists := cloned.GetString("password")
	require.True(t, exists)
	require.Equal(t, "unsaved-secret", password)
	require.NoError(t, cloned.SetSecret("password", "changed"))
	password, exists = view.GetString("password")
	require.True(t, exists)
	require.Equal(t, "unsaved-secret", password)
}

func TestEnvironmentConfigFileRoundTrip(t *testing.T) {
	for _, tt := range []struct {
		name   string
		secret bool
	}{
		{name: "plain value"},
		{name: "unsaved secret", secret: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", filepath.Join(dir, "user"))
			view := New("test").Config()
			if tt.secret {
				require.NoError(t, view.SetSecret("value", "original-value"))
			} else {
				require.NoError(t, view.Set("value", "original-value"))
			}

			manager := config.NewFileConfigManager(config.NewManager())
			path := filepath.Join(dir, "config.json")
			require.NoError(t, manager.Save(view, path))
			loaded, err := manager.Load(path)
			require.NoError(t, err)
			require.Equal(t, view.Raw(), loaded.Raw())
			value, exists := loaded.GetString("value")
			require.True(t, exists)
			require.Equal(t, "original-value", value)

			if tt.secret {
				contents, err := os.ReadFile(path)
				require.NoError(t, err)
				require.NotContains(t, string(contents), "original-value")
			}
		})
	}
}

func TestEnvironmentConfigReadsShareLock(t *testing.T) {
	env := New("test")
	view := env.Config()
	require.NoError(t, view.Set("nested", map[string]any{"list": []any{"value"}}))
	require.NoError(t, view.SetSecret("password", "secret"))

	env.mu.RLock()
	done := make(chan error, 1)
	go func() {
		view.Raw()
		view.ResolvedRaw()
		view.Get("nested")
		view.GetMap("nested")
		view.GetSlice("nested.list")
		view.GetString("password")
		view.IsEmpty()
		var section map[string]any
		_, err := view.GetSection("nested", &section)
		if err == nil {
			_, err = config.Clone(view)
		}
		done <- err
	}()
	select {
	case err := <-done:
		env.mu.RUnlock()
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		env.mu.RUnlock()
		<-done
		t.Fatal("config reads blocked another reader")
	}
}

func TestEnvironmentConfigConcurrentAccess(t *testing.T) {
	env := New("test")
	view := env.Config()
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 20 {
			require.NoError(t, env.ReplaceState(EnvironmentState{Config: config.NewEmptyConfig()}))
		}
	})
	for i := range 8 {
		wg.Go(func() {
			key := fmt.Sprintf("worker%d", i)
			for range 20 {
				require.NoError(t, view.Set(key, []map[string]string{{"key": "value"}}))
				view.Get(key)
				view.GetSlice(key)
				view.GetMap(key)
				view.GetString(key)
				view.Raw()
				view.ResolvedRaw()
				view.IsEmpty()
				var section any
				_, err := view.GetSection(key, &section)
				require.NoError(t, err)
				require.NoError(t, view.SetSecret(key+"Secret", "value"))
				_, err = config.Clone(view)
				require.NoError(t, err)
				require.NoError(t, view.Unset(key))
			}
		})
	}
	wg.Wait()
}

func TestEnvironmentConfigRejectsUnsupportedValues(t *testing.T) {
	env := New("test")
	view := env.Config()
	require.NoError(t, view.Set("existing", "original"))
	for _, value := range []any{
		make(chan int),
		func() {},
		struct{ hidden []string }{hidden: []string{"value"}},
		map[string]any{"nested": make(chan int)},
	} {
		require.Error(t, view.Set("existing", value))
		current, exists := view.GetString("existing")
		require.True(t, exists)
		require.Equal(t, "original", current)
	}
}
