package alpha

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

func Test_AlphaToggle(t *testing.T) {
	t.Parallel()

	t.Run("not alpha key", func(t *testing.T) {
		_, isAlpha := IsFeatureKey("this-is-not-an-alpha-key-and-should-never-be")
		require.False(t, isAlpha)
	})

	t.Run("list alpha features", func(t *testing.T) {
		alphaFeatureId := "some-id"
		alphaFeatureDescription := "some description"
		mockAlphaFeatures := func() []Feature {
			return []Feature{
				{Id: alphaFeatureId, Description: alphaFeatureDescription},
			}
		}

		mockConfig := config.NewConfig(map[string]any{
			parentKey: map[string]any{
				alphaFeatureId: enabledValue,
			},
		})

		// We don't need the user-config
		alphaManager := newFeatureManagerForTest(mockAlphaFeatures, mockConfig)
		alphaF, err := alphaManager.ListFeatures()
		require.NoError(t, err)
		require.True(t, len(alphaF) == 1)

		feature := alphaF[alphaFeatureId]
		require.Equal(t, alphaFeatureId, feature.Id)
		require.Equal(t, alphaFeatureDescription, feature.Description)
		require.Equal(t, enabledText, feature.Status)
	})

	t.Run("list alpha features off", func(t *testing.T) {
		alphaFeatureId := "some-id"
		alphaFeatureDescription := "some description"
		mockAlphaFeatures := func() []Feature {
			return []Feature{
				{Id: alphaFeatureId, Description: alphaFeatureDescription},
			}
		}

		mockConfig := config.NewConfig(map[string]any{
			parentKey: map[string]any{
				alphaFeatureId: disabledValue,
			},
		})

		// We don't need the user-config
		alphaManager := newFeatureManagerForTest(mockAlphaFeatures, mockConfig)
		alphaF, err := alphaManager.ListFeatures()
		require.NoError(t, err)
		require.True(t, len(alphaF) == 1)

		feature := alphaF[alphaFeatureId]
		require.Equal(t, alphaFeatureId, feature.Id)
		require.Equal(t, alphaFeatureDescription, feature.Description)
		require.Equal(t, disabledText, feature.Status)
	})

	t.Run("list alpha features many", func(t *testing.T) {
		alphaFeatureId := "some-id"
		alphaFeatureDescription := "some description"
		alphaFeatureIdOff := "some-id-off"
		alphaFeatureDescriptionOff := "some description-off"

		mockAlphaFeatures := func() []Feature {
			return []Feature{
				{Id: alphaFeatureId, Description: alphaFeatureDescription},
				{Id: alphaFeatureIdOff, Description: alphaFeatureDescriptionOff},
			}
		}

		mockConfig := config.NewConfig(map[string]any{
			parentKey: map[string]any{
				alphaFeatureId: enabledValue,
			},
		})

		// We don't need the user-config
		alphaManager := newFeatureManagerForTest(mockAlphaFeatures, mockConfig)
		alphaF, err := alphaManager.ListFeatures()
		require.NoError(t, err)
		require.True(t, len(alphaF) == 2)

		feature := alphaF[alphaFeatureId]
		require.Equal(t, alphaFeatureId, feature.Id)
		require.Equal(t, alphaFeatureDescription, feature.Description)
		require.Equal(t, enabledText, feature.Status)
		featureOff := alphaF[alphaFeatureIdOff]
		require.Equal(t, alphaFeatureIdOff, featureOff.Id)
		require.Equal(t, alphaFeatureDescriptionOff, featureOff.Description)
		require.Equal(t, disabledText, featureOff.Status)
	})

	t.Run("list alpha features all on", func(t *testing.T) {
		alphaFeatureId := "some-id"
		alphaFeatureDescription := "some description"
		alphaFeatureIdOff := "some-id-off"
		alphaFeatureDescriptionOff := "some description-off"

		mockAlphaFeatures := func() []Feature {
			return []Feature{
				{Id: alphaFeatureId, Description: alphaFeatureDescription},
				{Id: alphaFeatureIdOff, Description: alphaFeatureDescriptionOff},
			}
		}

		mockConfig := config.NewConfig(map[string]any{
			parentKey: map[string]any{
				string(AllId): enabledValue,
			},
		})

		// We don't need the user-config
		alphaManager := newFeatureManagerForTest(mockAlphaFeatures, mockConfig)
		alphaF, err := alphaManager.ListFeatures()
		require.NoError(t, err)
		require.True(t, len(alphaF) == 2)

		feature := alphaF[alphaFeatureId]
		require.Equal(t, alphaFeatureId, feature.Id)
		require.Equal(t, alphaFeatureDescription, feature.Description)
		require.Equal(t, enabledText, feature.Status)
		featureOff := alphaF[alphaFeatureIdOff]
		require.Equal(t, alphaFeatureIdOff, featureOff.Id)
		require.Equal(t, alphaFeatureDescriptionOff, featureOff.Description)
		require.Equal(t, enabledText, featureOff.Status)
	})

	t.Run("cover constructor", func(t *testing.T) {
		_ = NewFeaturesManager(config.NewUserConfigManager(config.NewFileConfigManager(config.NewManager())))
	})

}

func Test_AlphaFeature_IsEnabled(t *testing.T) {
	mockAlphaFeatures := []Feature{}
	for i := 1; i <= 10; i++ {
		mockAlphaFeatures = append(mockAlphaFeatures, Feature{
			Id:          fmt.Sprintf("category.feature.%d", i),
			Description: fmt.Sprintf("Description for feature %d", i),
		})
	}

	featuresResolver := func() []Feature {
		return mockAlphaFeatures
	}

	mockConfig := config.NewConfig(nil)

	// Enable even numbered features
	for i := 1; i <= 10; i++ {
		if i%2 == 0 {
			featureId := fmt.Sprintf("alpha.category.feature.%d", i)
			mockConfig.Set(featureId, "on")
		}
	}

	alphaManager := newFeatureManagerForTest(featuresResolver, mockConfig)

	t.Run("enabled from config", func(t *testing.T) {
		actual := alphaManager.IsEnabled(FeatureId("category.feature.2"))
		require.True(t, actual)
	})

	t.Run("disabled from config", func(t *testing.T) {
		actual := alphaManager.IsEnabled(FeatureId("category.feature.1"))
		require.False(t, actual)
	})

	t.Run("enabled from env var", func(t *testing.T) {
		os.Setenv("AZD_ALPHA_ENABLE_CATEGORY_FEATURE_3", "true")
		actual := alphaManager.IsEnabled(FeatureId("category.feature.3"))
		require.True(t, actual)
	})

	t.Run("enabled from default", func(t *testing.T) {
		SetDefaultEnablement("category.feature.9", true)
		actual := alphaManager.IsEnabled(FeatureId("category.feature.9"))
		require.True(t, actual)
	})
}

func newFeatureManagerForTest(alphaFeatureResolver func() []Feature, config config.Config) *FeatureManager {
	return &FeatureManager{
		alphaFeaturesResolver: alphaFeatureResolver,
		userConfigCache:       config,
		withSync:              &sync.Once{},
	}
}
