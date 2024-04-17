package config

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/stretchr/testify/require"
)

func Test_SetGetUnsetWithValue(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		value any
	}{
		{
			name:  "RootValue",
			path:  "a",
			value: "apple",
		},
		{
			name:  "NestedValue",
			path:  "defaults.location",
			value: "westus",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			azdConfig := NewEmptyConfig()
			err := azdConfig.Set(test.path, test.value)
			require.NoError(t, err)

			value, ok := azdConfig.Get(test.path)
			require.True(t, ok)
			require.Equal(t, test.value, value)

			err = azdConfig.Unset(test.path)
			require.NoError(t, err)

			value, ok = azdConfig.Get(test.path)
			require.Nil(t, value)
			require.False(t, ok)
		})
	}
}

func Test_SetGetUnsetRootNodeWithChildren(t *testing.T) {
	expectedLocation := "westus2"
	expectedEmail := "john.doe@contoso.com"

	azdConfig := NewEmptyConfig()
	_ = azdConfig.Set("defaults.location", expectedLocation)
	_ = azdConfig.Set("defaults.subscription", "SUBSCRIPTION_ID")
	_ = azdConfig.Set("user.email", expectedEmail)

	location, ok := azdConfig.Get("defaults.location")
	require.True(t, ok)
	require.Equal(t, expectedLocation, location)

	email, ok := azdConfig.Get("user.email")
	require.True(t, ok)
	require.Equal(t, expectedEmail, email)

	// Remove the whole defaults object
	err := azdConfig.Unset("defaults")
	require.NoError(t, err)

	// Location should not exist
	location, ok = azdConfig.Get("defaults.location")
	require.False(t, ok)
	require.Nil(t, location)

	// user.email should still exist
	email, ok = azdConfig.Get("user.email")
	require.True(t, ok)
	require.Equal(t, expectedEmail, email)
}

func Test_IsEmpty(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		azdConfig := NewEmptyConfig()
		require.True(t, azdConfig.IsEmpty())
	})

	t.Run("EmptyWithEmptyMap", func(t *testing.T) {
		azdConfig := NewConfig(map[string]any{})
		require.True(t, azdConfig.IsEmpty())
	})

	t.Run("NotEmpty", func(t *testing.T) {
		azdConfig := NewConfig(map[string]any{
			"a": "apple",
		})
		require.False(t, azdConfig.IsEmpty())
	})
}

func Test_GetString(t *testing.T) {
	t.Run("ValidString", func(t *testing.T) {
		azdConfig := NewEmptyConfig()
		err := azdConfig.Set("a.b.c", "apple")
		require.NoError(t, err)

		value, ok := azdConfig.GetString("a.b.c")
		require.Equal(t, "apple", value)
		require.True(t, ok)
	})

	t.Run("EmptyString", func(t *testing.T) {
		azdConfig := NewEmptyConfig()
		err := azdConfig.Set("a.b.c", "")
		require.NoError(t, err)

		value, ok := azdConfig.GetString("a.b.c")
		require.Equal(t, "", value)
		require.True(t, ok)
	})

	t.Run("NonStringValue", func(t *testing.T) {
		azdConfig := NewEmptyConfig()
		err := azdConfig.Set("a.b.c", 1)
		require.NoError(t, err)

		value, ok := azdConfig.GetString("a.b.c")
		require.Equal(t, "", value)
		require.False(t, ok)
	})

	t.Run("NilValue", func(t *testing.T) {
		azdConfig := NewEmptyConfig()
		err := azdConfig.Set("a.b.c", nil)
		require.NoError(t, err)

		value, ok := azdConfig.GetString("a.b.c")
		require.Equal(t, "", value)
		require.False(t, ok)
	})
}

func Test_GetSection(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := &testConfig{
			A: "apple",
			B: "banana",
			C: "cherry",
		}

		values, err := convert.ToMap(expected)
		require.NoError(t, err)

		azdConfig := NewEmptyConfig()
		err = azdConfig.Set("parent.section", values)
		require.NoError(t, err)

		var actual *testConfig
		ok, err := azdConfig.GetSection("parent.section", &actual)
		require.True(t, ok)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	})

	t.Run("NotFound", func(t *testing.T) {
		azdConfig := NewEmptyConfig()

		var actual *testConfig
		ok, err := azdConfig.GetSection("parent.section", &actual)
		require.False(t, ok)
		require.NoError(t, err)
	})

	t.Run("NotStruct", func(t *testing.T) {
		azdConfig := NewEmptyConfig()
		err := azdConfig.Set("parent.section", "apple")
		require.NoError(t, err)

		var actual *testConfig
		ok, err := azdConfig.GetSection("parent.section", &actual)
		require.True(t, ok)
		require.Error(t, err)
	})

	t.Run("Empty", func(t *testing.T) {
		azdConfig := NewEmptyConfig()
		err := azdConfig.Set("parent.section", map[string]any{})
		require.NoError(t, err)

		var actual *testConfig
		ok, err := azdConfig.GetSection("parent.section", &actual)
		require.True(t, ok)
		require.NoError(t, err)
		require.Equal(t, "", actual.A)
		require.Equal(t, "", actual.B)
		require.Equal(t, "", actual.C)
	})

	t.Run("PartialSection", func(t *testing.T) {
		azdConfig := NewEmptyConfig()
		err := azdConfig.Set("parent.section.A", "apple")
		require.NoError(t, err)
		err = azdConfig.Set("parent.section.B", "banana")
		require.NoError(t, err)

		var actual *testConfig
		ok, err := azdConfig.GetSection("parent.section", &actual)
		require.True(t, ok)
		require.NoError(t, err)
		require.Equal(t, "apple", actual.A)
		require.Equal(t, "banana", actual.B)
		require.Equal(t, "", actual.C)
	})

	t.Run("ExtraProps", func(t *testing.T) {
		azdConfig := NewEmptyConfig()
		err := azdConfig.Set("parent.section.A", "apple")
		require.NoError(t, err)
		err = azdConfig.Set("parent.section.B", "banana")
		require.NoError(t, err)
		err = azdConfig.Set("parent.section.C", "cherry")
		require.NoError(t, err)
		err = azdConfig.Set("parent.section.D", "durian")
		require.NoError(t, err)

		var actual *testConfig
		ok, err := azdConfig.GetSection("parent.section", &actual)
		require.True(t, ok)
		require.NoError(t, err)
		require.Equal(t, "apple", actual.A)
		require.Equal(t, "banana", actual.B)
		require.Equal(t, "cherry", actual.C)
	})
}

type testConfig struct {
	A string
	B string
	C string
}

func Test_Paths(t *testing.T) {
	tests := []struct {
		name     string
		config   *config
		expected []string
	}{
		{
			name: "EmptyConfig",
			config: &config{
				data: map[string]any{},
			},
			expected: []string{},
		},
		{
			name: "ConfigWith1Paths",
			config: &config{
				data: map[string]any{
					"paths": []string{
						"/path1",
						"/path2",
						"/path3",
					},
				},
			},
			expected: []string{
				"paths",
			},
		},
		{
			name: "ConfigWith2levels",
			config: &config{
				data: map[string]any{
					"path1": map[string]any{
						"level1": "level1",
					},
					"path2": "value2",
				},
			},
			expected: []string{
				"path1.level1", "path2",
			},
		},
		{
			name: "ConfigWith3levelsPaths",
			config: &config{
				data: map[string]any{
					"path1": map[string]any{
						"level1": "level1",
					},
					"path2": "value2",
					"path3": map[string]any{
						"level1": map[string]any{
							"level2": "level2",
						},
					},
				},
			},
			expected: []string{
				"path1.level1", "path2", "path3.level1.level2",
			},
		},
		{
			name: "ConfigWith2levelstop",
			config: &config{
				data: map[string]any{
					"path1": map[string]any{
						"level1": "level1",
						"level2": "level1",
					},
				},
			},
			expected: []string{
				"path1.level1", "path1.level2",
			},
		},
		{
			name: "ConfigWith2levelstop",
			config: &config{
				data: map[string]any{
					"path1": map[string]any{
						"level1": "level1",
						"level2": "level1",
					},
					"path2": map[string]any{
						"level1": "level1",
						"level2": map[string]any{
							"level3":  "level3",
							"level31": "level31",
						},
					},
				},
			},
			expected: []string{
				"path1.level1", "path1.level2", "path2.level1", "path2.level2.level3", "path2.level2.level31",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := test.config.Paths()
			for _, path := range test.expected {
				require.Contains(t, paths, path)
			}
		})
	}
}
