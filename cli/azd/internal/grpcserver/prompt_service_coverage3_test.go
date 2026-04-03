// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- convertToInt32 / convertToInt tests ---

func TestConvertToInt32_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, convertToInt32(nil))
}

func TestConvertToInt32_Value(t *testing.T) {
	t.Parallel()
	val := 42
	result := convertToInt32(&val)
	require.NotNil(t, result)
	require.Equal(t, int32(42), *result)
}

func TestConvertToInt32_Zero(t *testing.T) {
	t.Parallel()
	val := 0
	result := convertToInt32(&val)
	require.NotNil(t, result)
	require.Equal(t, int32(0), *result)
}

func TestConvertToInt32_Negative(t *testing.T) {
	t.Parallel()
	val := -7
	result := convertToInt32(&val)
	require.NotNil(t, result)
	require.Equal(t, int32(-7), *result)
}

func TestConvertToInt_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, convertToInt(nil))
}

func TestConvertToInt_Value(t *testing.T) {
	t.Parallel()
	val := int32(99)
	result := convertToInt(&val)
	require.NotNil(t, result)
	require.Equal(t, 99, *result)
}

func TestConvertToInt_Zero(t *testing.T) {
	t.Parallel()
	val := int32(0)
	result := convertToInt(&val)
	require.NotNil(t, result)
	require.Equal(t, 0, *result)
}

// --- requirePromptSubscriptionID tests ---

func TestRequirePromptSubscriptionID_NilContext(t *testing.T) {
	t.Parallel()
	_, err := requirePromptSubscriptionID(nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRequirePromptSubscriptionID_NilScope(t *testing.T) {
	t.Parallel()
	_, err := requirePromptSubscriptionID(&azdext.AzureContext{})
	require.Error(t, err)
}

func TestRequirePromptSubscriptionID_EmptySubscriptionID(t *testing.T) {
	t.Parallel()
	_, err := requirePromptSubscriptionID(&azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: ""},
	})
	require.Error(t, err)
}

func TestRequirePromptSubscriptionID_Valid(t *testing.T) {
	t.Parallel()
	subId, err := requirePromptSubscriptionID(&azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: "sub-123"},
	})
	require.NoError(t, err)
	require.Equal(t, "sub-123", subId)
}

// --- requireSubscriptionID tests (ai_model_service helpers) ---

func TestRequireSubscriptionID_NilContext(t *testing.T) {
	t.Parallel()
	_, err := requireSubscriptionID(nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRequireSubscriptionID_NilScope(t *testing.T) {
	t.Parallel()
	_, err := requireSubscriptionID(&azdext.AzureContext{})
	require.Error(t, err)
}

func TestRequireSubscriptionID_EmptySubscriptionID(t *testing.T) {
	t.Parallel()
	_, err := requireSubscriptionID(&azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: ""},
	})
	require.Error(t, err)
}

func TestRequireSubscriptionID_Valid(t *testing.T) {
	t.Parallel()
	subId, err := requireSubscriptionID(&azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: "sub-abc"},
	})
	require.NoError(t, err)
	require.Equal(t, "sub-abc", subId)
}

// --- protoToFilterOptions tests ---

func TestProtoToFilterOptions_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, protoToFilterOptions(nil))
}

func TestProtoToFilterOptions_WithValues(t *testing.T) {
	t.Parallel()
	opts := protoToFilterOptions(&azdext.AiModelFilterOptions{
		Locations:         []string{"eastus", "westus"},
		Capabilities:      []string{"chat"},
		Formats:           []string{"json"},
		Statuses:          []string{"active"},
		ExcludeModelNames: []string{"gpt-3"},
	})
	require.NotNil(t, opts)
	require.Equal(t, []string{"eastus", "westus"}, opts.Locations)
	require.Equal(t, []string{"chat"}, opts.Capabilities)
	require.Equal(t, []string{"json"}, opts.Formats)
	require.Equal(t, []string{"active"}, opts.Statuses)
	require.Equal(t, []string{"gpt-3"}, opts.ExcludeModelNames)
}

// --- protoToDeploymentOptions tests ---

func TestProtoToDeploymentOptions_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, protoToDeploymentOptions(nil))
}

func TestProtoToDeploymentOptions_WithValues(t *testing.T) {
	t.Parallel()
	cap := int32(100)
	opts := protoToDeploymentOptions(&azdext.AiModelDeploymentOptions{
		Locations: []string{"eastus"},
		Versions:  []string{"v1"},
		Skus:      []string{"S0"},
		Capacity:  &cap,
	})
	require.NotNil(t, opts)
	require.Equal(t, []string{"eastus"}, opts.Locations)
	require.Equal(t, []string{"v1"}, opts.Versions)
	require.Equal(t, []string{"S0"}, opts.Skus)
	require.NotNil(t, opts.Capacity)
	require.Equal(t, int32(100), *opts.Capacity)
}

func TestProtoToDeploymentOptions_NoCapacity(t *testing.T) {
	t.Parallel()
	opts := protoToDeploymentOptions(&azdext.AiModelDeploymentOptions{
		Locations: []string{"eastus"},
	})
	require.NotNil(t, opts)
	require.Nil(t, opts.Capacity)
}

// --- protoToQuotaCheckOptions tests ---

func TestProtoToQuotaCheckOptions_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, protoToQuotaCheckOptions(nil))
}

func TestProtoToQuotaCheckOptions_WithValues(t *testing.T) {
	t.Parallel()
	opts := protoToQuotaCheckOptions(&azdext.QuotaCheckOptions{
		MinRemainingCapacity: 50.0,
	})
	require.NotNil(t, opts)
	require.Equal(t, 50.0, opts.MinRemainingCapacity)
}

// --- buildAgentOptions tests ---

func TestBuildAgentOptions_Defaults(t *testing.T) {
	t.Parallel()
	opts := buildAgentOptions("", "", "", "", false, false)
	require.Len(t, opts, 1) // only WithHeadless(false)
}

func TestBuildAgentOptions_AllSet(t *testing.T) {
	t.Parallel()
	opts := buildAgentOptions("gpt-4o", "high", "You are helpful", "plan", true, true)
	// WithHeadless(true) + WithModel + WithReasoningEffort + WithSystemMessage + WithMode + WithDebug
	require.Len(t, opts, 6)
}

func TestBuildAgentOptions_Partial(t *testing.T) {
	t.Parallel()
	opts := buildAgentOptions("gpt-4o", "", "", "", false, true)
	// WithHeadless(true) + WithModel("gpt-4o")
	require.Len(t, opts, 2)
}

// --- convertFileChangeType tests ---

func TestConvertFileChangeType_Created(t *testing.T) {
	t.Parallel()
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_CREATED,
		convertFileChangeType(watch.FileCreated))
}

func TestConvertFileChangeType_Modified(t *testing.T) {
	t.Parallel()
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_MODIFIED,
		convertFileChangeType(watch.FileModified))
}

func TestConvertFileChangeType_Deleted(t *testing.T) {
	t.Parallel()
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_DELETED,
		convertFileChangeType(watch.FileDeleted))
}

func TestConvertFileChangeType_Unknown(t *testing.T) {
	t.Parallel()
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_UNSPECIFIED,
		convertFileChangeType(watch.FileChangeType(999)))
}

// --- convertFileChanges tests ---

func TestConvertFileChanges_Empty(t *testing.T) {
	t.Parallel()
	result := convertFileChanges(nil)
	require.Nil(t, result)

	result = convertFileChanges([]watch.FileChange{})
	require.Nil(t, result)
}

func TestConvertFileChanges_WithChanges(t *testing.T) {
	t.Parallel()
	changes := []watch.FileChange{
		{Path: "/tmp/test.go", ChangeType: watch.FileCreated},
		{Path: "/tmp/test2.go", ChangeType: watch.FileModified},
	}
	result := convertFileChanges(changes)
	require.Len(t, result, 2)
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_CREATED, result[0].ChangeType)
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_MODIFIED, result[1].ChangeType)
}

// --- convertUsageMetrics tests ---

func TestConvertUsageMetrics(t *testing.T) {
	t.Parallel()
	usage := agent.UsageMetrics{
		Model:           "gpt-4o",
		InputTokens:     100,
		OutputTokens:    50,
		BillingRate:     0.5,
		PremiumRequests: 2,
		DurationMS:      1500,
	}
	result := convertUsageMetrics(usage)
	require.Equal(t, "gpt-4o", result.Model)
	require.Equal(t, float64(100), result.InputTokens)
	require.Equal(t, float64(50), result.OutputTokens)
	require.Equal(t, float64(150), result.TotalTokens) // 100 + 50
	require.Equal(t, 0.5, result.BillingRate)
	require.Equal(t, float64(2), result.PremiumRequests)
	require.Equal(t, float64(1500), result.DurationMs)
}

// --- convertSessionEvent tests ---

func TestConvertSessionEvent_BasicFields(t *testing.T) {
	t.Parallel()
	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	event := agent.SessionEvent{
		Type:      copilot.SessionEventType("test_event"),
		Timestamp: ts,
		Data:      copilot.Data{},
	}
	result := convertSessionEvent(event)
	require.Equal(t, "test_event", result.Type)
	require.Equal(t, "2024-01-15T10:30:00.000Z", result.Timestamp)
}

func TestConvertSessionEvent_WithProducer(t *testing.T) {
	t.Parallel()
	producer := "test-agent"
	event := agent.SessionEvent{
		Type:      copilot.SessionEventType("init"),
		Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Data: copilot.Data{
			Producer: &producer,
		},
	}
	result := convertSessionEvent(event)
	require.Equal(t, "init", result.Type)
	require.NotNil(t, result.Data)
	require.Equal(t, producer, result.Data.Fields["producer"].GetStringValue())
}

func TestConvertSessionEvent_WithSelectedModel(t *testing.T) {
	t.Parallel()
	model := "gpt-4o"
	event := agent.SessionEvent{
		Type:      copilot.SessionEventType("session_start"),
		Timestamp: time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
		Data: copilot.Data{
			SelectedModel: &model,
		},
	}
	result := convertSessionEvent(event)
	require.Equal(t, "session_start", result.Type)
	require.NotNil(t, result.Data)
}

// --- modelQuotaSummary tests ---

func TestModelQuotaSummary_NoVersions(t *testing.T) {
	t.Parallel()
	model := ai.AiModel{Name: "gpt-4o"}
	result := modelQuotaSummary(model, nil)
	require.Equal(t, output.WithGrayFormat("[no quota info]"), result)
}

func TestModelQuotaSummary_NoMatchingUsage(t *testing.T) {
	t.Parallel()
	model := ai.AiModel{
		Name: "gpt-4o",
		Versions: []ai.AiModelVersion{
			{Skus: []ai.AiModelSku{{UsageName: "sku-1"}}},
		},
	}
	usageMap := map[string]ai.AiModelUsage{}
	result := modelQuotaSummary(model, usageMap)
	require.Equal(t, output.WithGrayFormat("[no quota info]"), result)
}

func TestModelQuotaSummary_WithQuota(t *testing.T) {
	t.Parallel()
	model := ai.AiModel{
		Name: "gpt-4o",
		Versions: []ai.AiModelVersion{
			{Skus: []ai.AiModelSku{
				{UsageName: "sku-1"},
				{UsageName: "sku-2"},
			}},
		},
	}
	usageMap := map[string]ai.AiModelUsage{
		"sku-1": {Limit: 1000, CurrentValue: 200},
		"sku-2": {Limit: 500, CurrentValue: 100},
	}
	result := modelQuotaSummary(model, usageMap)
	require.Equal(t, output.WithGrayFormat("[up to %.0f quota available]", float64(800)), result)
}

// --- selectModelNoPrompt tests ---

func TestSelectModelNoPrompt_EmptyDefault(t *testing.T) {
	t.Parallel()
	models := []ai.AiModel{{Name: "gpt-4o"}}
	_, err := selectModelNoPrompt(models, "")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestSelectModelNoPrompt_MatchFound(t *testing.T) {
	t.Parallel()
	models := []ai.AiModel{
		{Name: "gpt-3.5"},
		{Name: "gpt-4o"},
	}
	resp, err := selectModelNoPrompt(models, "GPT-4O") // case-insensitive
	require.NoError(t, err)
	require.NotNil(t, resp.Model)
}

func TestSelectModelNoPrompt_NoMatch(t *testing.T) {
	t.Parallel()
	models := []ai.AiModel{{Name: "gpt-4o"}}
	_, err := selectModelNoPrompt(models, "nonexistent")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

// --- findDefaultIndex tests ---

func TestFindDefaultIndex_Empty(t *testing.T) {
	t.Parallel()
	result := findDefaultIndex(nil, "test")
	require.Nil(t, result)
}

func TestFindDefaultIndex_EmptyDefault(t *testing.T) {
	t.Parallel()
	choices := []*ux.SelectChoice{{Value: "a"}}
	result := findDefaultIndex(choices, "")
	require.Nil(t, result)
}

func TestFindDefaultIndex_Found(t *testing.T) {
	t.Parallel()
	choices := []*ux.SelectChoice{
		{Value: "alpha"},
		{Value: "beta"},
		{Value: "gamma"},
	}
	result := findDefaultIndex(choices, "BETA") // case-insensitive
	require.NotNil(t, result)
	require.Equal(t, 1, *result)
}

func TestFindDefaultIndex_NotFound(t *testing.T) {
	t.Parallel()
	choices := []*ux.SelectChoice{
		{Value: "alpha"},
		{Value: "beta"},
	}
	result := findDefaultIndex(choices, "delta")
	require.Nil(t, result)
}

// --- maxSkuCandidateRemaining tests ---

func TestMaxSkuCandidateRemaining_Empty(t *testing.T) {
	t.Parallel()
	_, found := maxSkuCandidateRemaining(nil)
	require.False(t, found)
}

func TestMaxSkuCandidateRemaining_AllNilRemaining(t *testing.T) {
	t.Parallel()
	candidates := []skuCandidate{
		{remaining: nil},
		{remaining: nil},
	}
	_, found := maxSkuCandidateRemaining(candidates)
	require.False(t, found)
}

func TestMaxSkuCandidateRemaining_WithValues(t *testing.T) {
	t.Parallel()
	r1 := float64(100)
	r2 := float64(500)
	r3 := float64(200)
	candidates := []skuCandidate{
		{remaining: &r1},
		{remaining: &r2},
		{remaining: &r3},
	}
	max, found := maxSkuCandidateRemaining(candidates)
	require.True(t, found)
	require.Equal(t, float64(500), max)
}

func TestMaxSkuCandidateRemaining_MixedNilAndValues(t *testing.T) {
	t.Parallel()
	r1 := float64(300)
	candidates := []skuCandidate{
		{remaining: nil},
		{remaining: &r1},
		{remaining: nil},
	}
	max, found := maxSkuCandidateRemaining(candidates)
	require.True(t, found)
	require.Equal(t, float64(300), max)
}

// --- buildSkuCandidatesForVersion tests ---

func TestBuildSkuCandidatesForVersion_EmptySkus(t *testing.T) {
	t.Parallel()
	version := ai.AiModelVersion{}
	result := buildSkuCandidatesForVersion(version, nil, nil, nil, false)
	require.Empty(t, result)
}

func TestBuildSkuCandidatesForVersion_NoQuotaCheck(t *testing.T) {
	t.Parallel()
	version := ai.AiModelVersion{
		Skus: []ai.AiModelSku{
			{Name: "S0", UsageName: "openai-standard"},
			{Name: "P1", UsageName: "openai-provisioned"},
		},
	}
	result := buildSkuCandidatesForVersion(version, nil, nil, nil, false)
	require.Len(t, result, 2)
}

func TestBuildSkuCandidatesForVersion_SkuFilter(t *testing.T) {
	t.Parallel()
	version := ai.AiModelVersion{
		Skus: []ai.AiModelSku{
			{Name: "S0", UsageName: "standard"},
			{Name: "P1", UsageName: "provisioned"},
		},
	}
	options := &ai.DeploymentOptions{Skus: []string{"S0"}}
	result := buildSkuCandidatesForVersion(version, options, nil, nil, false)
	require.Len(t, result, 1)
	require.Equal(t, "S0", result[0].sku.Name)
}

// --- validateDeploymentCapacity tests ---

func TestValidateDeploymentCapacity_Invalid(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{}
	_, err := validateDeploymentCapacity("abc", sku)
	require.Error(t, err)
	require.Contains(t, err.Error(), "whole number")
}

func TestValidateDeploymentCapacity_Zero(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{}
	_, err := validateDeploymentCapacity("0", sku)
	require.Error(t, err)
	require.Contains(t, err.Error(), "greater than 0")
}

func TestValidateDeploymentCapacity_BelowMin(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{MinCapacity: 10}
	_, err := validateDeploymentCapacity("5", sku)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 10")
}

func TestValidateDeploymentCapacity_AboveMax(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{MaxCapacity: 100}
	_, err := validateDeploymentCapacity("200", sku)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most 100")
}

func TestValidateDeploymentCapacity_WrongStep(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{CapacityStep: 10}
	_, err := validateDeploymentCapacity("15", sku)
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple of 10")
}

func TestValidateDeploymentCapacity_Valid(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{MinCapacity: 10, MaxCapacity: 100, CapacityStep: 10}
	cap, err := validateDeploymentCapacity("50", sku)
	require.NoError(t, err)
	require.Equal(t, int32(50), cap)
}

// --- validateCapacityAgainstRemainingQuota tests ---

func TestValidateCapacityAgainstRemainingQuota_NilRemaining(t *testing.T) {
	t.Parallel()
	err := validateCapacityAgainstRemainingQuota(100, nil)
	require.NoError(t, err)
}

func TestValidateCapacityAgainstRemainingQuota_Exceeds(t *testing.T) {
	t.Parallel()
	remaining := float64(50)
	err := validateCapacityAgainstRemainingQuota(100, &remaining)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most 50")
}

func TestValidateCapacityAgainstRemainingQuota_WithinLimit(t *testing.T) {
	t.Parallel()
	remaining := float64(200)
	err := validateCapacityAgainstRemainingQuota(100, &remaining)
	require.NoError(t, err)
}

// --- createAzureContext tests ---

func TestCreateAzureContext_NilWire(t *testing.T) {
	t.Parallel()
	svc := &promptService{}
	_, err := svc.createAzureContext(nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateAzureContext_NilScope(t *testing.T) {
	t.Parallel()
	svc := &promptService{}
	_, err := svc.createAzureContext(&azdext.AzureContext{})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateAzureContext_InvalidResourceID(t *testing.T) {
	t.Parallel()
	svc := &promptService{}
	_, err := svc.createAzureContext(&azdext.AzureContext{
		Scope:     &azdext.AzureScope{SubscriptionId: "sub-1"},
		Resources: []string{"not-a-valid-resource-id"},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

// --- createResourceOptions tests ---

func TestCreateResourceOptions_Nil(t *testing.T) {
	t.Parallel()
	opts := createResourceOptions(nil)
	require.Nil(t, opts.ResourceType)
}

func TestCreateResourceOptions_WithValues(t *testing.T) {
	t.Parallel()
	opts := createResourceOptions(&azdext.PromptResourceOptions{
		ResourceType:            "Microsoft.Web/sites",
		Kinds:                   []string{"web"},
		ResourceTypeDisplayName: "Web App",
		SelectOptions: &azdext.PromptResourceSelectOptions{
			Message:     "Select a web app",
			HelpMessage: "Choose one",
		},
	})
	require.NotNil(t, opts.ResourceType)
	require.Equal(t, []string{"web"}, opts.Kinds)
	require.Equal(t, "Web App", opts.ResourceTypeDisplayName)
	require.NotNil(t, opts.SelectorOptions)
	require.Equal(t, "Select a web app", opts.SelectorOptions.Message)
}

// --- createResourceGroupOptions tests ---

func TestCreateResourceGroupOptions_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, createResourceGroupOptions(nil))
}

func TestCreateResourceGroupOptions_NilSelectOptions(t *testing.T) {
	t.Parallel()
	require.Nil(t, createResourceGroupOptions(&azdext.PromptResourceGroupOptions{}))
}

func TestCreateResourceGroupOptions_WithValues(t *testing.T) {
	t.Parallel()
	allowNew := true
	result := createResourceGroupOptions(&azdext.PromptResourceGroupOptions{
		SelectOptions: &azdext.PromptResourceSelectOptions{
			Message:          "Select RG",
			AllowNewResource: &allowNew,
			DisplayCount:     10,
		},
	})
	require.NotNil(t, result)
	require.NotNil(t, result.SelectorOptions)
	require.Equal(t, "Select RG", result.SelectorOptions.Message)
	require.NotNil(t, result.SelectorOptions.AllowNewResource)
	require.True(t, *result.SelectorOptions.AllowNewResource)
	require.Equal(t, 10, result.SelectorOptions.DisplayCount)
}

// --- promptLock tests ---

func TestNewPromptLock(t *testing.T) {
	t.Parallel()
	lock := newPromptLock()
	require.NotNil(t, lock)
	require.NotNil(t, lock.ch)
}

func TestAcquirePromptLock_Success(t *testing.T) {
	t.Parallel()
	svc := &promptService{lock: newPromptLock()}
	release, err := svc.acquirePromptLock(t.Context())
	require.NoError(t, err)
	require.NotNil(t, release)

	// Release the lock
	release()
}

func TestAcquirePromptLock_CancelledContext(t *testing.T) {
	t.Parallel()
	svc := &promptService{lock: newPromptLock()}

	// Acquire the lock first
	release1, err := svc.acquirePromptLock(t.Context())
	require.NoError(t, err)

	// Try to acquire with a cancelled context
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	_, err = svc.acquirePromptLock(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)

	release1()
}

// --- PromptAi* method tests (validation paths) ---

func TestPromptService_PromptAiModel_NilSubscription(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiModel(t.Context(), &azdext.PromptAiModelRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestPromptService_PromptAiDeployment_NilSubscription(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiDeployment(t.Context(), &azdext.PromptAiDeploymentRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestPromptService_PromptAiDeployment_QuotaRequiresOneLocation(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiDeployment(t.Context(), &azdext.PromptAiDeploymentRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub-123"},
		},
		ModelName: "gpt-4",
		Quota:     &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		Options:   nil, // no locations
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "quota checking requires exactly one effective location")
}

func TestPromptService_PromptAiDeployment_QuotaWithMultipleLocations(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiDeployment(t.Context(), &azdext.PromptAiDeploymentRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub-123"},
		},
		ModelName: "gpt-4",
		Quota:     &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		Options:   &azdext.AiModelDeploymentOptions{Locations: []string{"eastus", "westus"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "quota checking requires exactly one effective location")
}

func TestPromptService_PromptAiLocationWithQuota_NilSubscription(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiLocationWithQuota(t.Context(), &azdext.PromptAiLocationWithQuotaRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestPromptService_PromptAiModelLocationWithQuota_NilSubscription(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiModelLocationWithQuota(t.Context(), &azdext.PromptAiModelLocationWithQuotaRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestPromptService_PromptAiModelLocationWithQuota_EmptyModelName(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiModelLocationWithQuota(t.Context(), &azdext.PromptAiModelLocationWithQuotaRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub-123"},
		},
		ModelName: "",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "model_name is required")
}
