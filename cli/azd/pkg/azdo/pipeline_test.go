// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"fmt"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/taskagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectAgentQueue(t *testing.T) {
	t.Run("no queues returns error", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()

		queue, err := selectAgentQueue(t.Context(), "project-1", nil, mockConsole)
		assert.Nil(t, queue)
		assert.ErrorContains(t, err, "no agent queues available in project project-1")
	})

	t.Run("empty queues returns error", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()

		queue, err := selectAgentQueue(t.Context(), "project-1", []taskagent.TaskAgentQueue{}, mockConsole)
		assert.Nil(t, queue)
		assert.ErrorContains(t, err, "no agent queues available in project project-1")
	})

	t.Run("queues with nil names are filtered out", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()
		queueId := 1
		queues := []taskagent.TaskAgentQueue{
			{Id: &queueId, Name: nil},
		}

		queue, err := selectAgentQueue(t.Context(), "project-1", queues, mockConsole)
		assert.Nil(t, queue)
		assert.ErrorContains(t, err, "no agent queues available in project project-1")
	})

	t.Run("queues with nil id are filtered out", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()
		name := "Default"
		queues := []taskagent.TaskAgentQueue{
			{Id: nil, Name: &name},
		}

		queue, err := selectAgentQueue(t.Context(), "project-1", queues, mockConsole)
		assert.Nil(t, queue)
		assert.ErrorContains(t, err, "no agent queues available in project project-1")
	})

	t.Run("nil name queues filtered leaving one valid", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()
		id1 := 1
		id2 := 2
		validName := "Azure Pipelines"
		queues := []taskagent.TaskAgentQueue{
			{Id: &id1, Name: nil},
			{Id: &id2, Name: &validName},
		}

		queue, err := selectAgentQueue(t.Context(), "project-1", queues, mockConsole)
		require.NoError(t, err)
		require.NotNil(t, queue)
		assert.Equal(t, "Azure Pipelines", *queue.Name)
	})

	t.Run("single queue auto-selects", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()
		queueName := "Azure Pipelines"
		queueId := 1
		queues := []taskagent.TaskAgentQueue{
			{Id: &queueId, Name: &queueName},
		}

		queue, err := selectAgentQueue(t.Context(), "project-1", queues, mockConsole)
		require.NoError(t, err)
		require.NotNil(t, queue)
		assert.Equal(t, "Azure Pipelines", *queue.Name)
		assert.Equal(t, 1, *queue.Id)
	})

	t.Run("multiple queues prompts user", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()
		mockConsole.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "agent queue")
		}).Respond(1) // select second queue

		name1 := "Default"
		id1 := 1
		name2 := "Azure Pipelines"
		id2 := 2
		queues := []taskagent.TaskAgentQueue{
			{Id: &id1, Name: &name1},
			{Id: &id2, Name: &name2},
		}

		queue, err := selectAgentQueue(t.Context(), "project-1", queues, mockConsole)
		require.NoError(t, err)
		require.NotNil(t, queue)
		assert.Equal(t, "Azure Pipelines", *queue.Name)
		assert.Equal(t, 2, *queue.Id)
	})

	t.Run("multiple queues selects first", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()
		mockConsole.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "agent queue")
		}).Respond(0) // select first queue

		name1 := "Default"
		id1 := 1
		name2 := "Azure Pipelines"
		id2 := 2
		queues := []taskagent.TaskAgentQueue{
			{Id: &id1, Name: &name1},
			{Id: &id2, Name: &name2},
		}

		queue, err := selectAgentQueue(t.Context(), "project-1", queues, mockConsole)
		require.NoError(t, err)
		require.NotNil(t, queue)
		assert.Equal(t, "Default", *queue.Name)
		assert.Equal(t, 1, *queue.Id)
	})

	t.Run("select error is wrapped", func(t *testing.T) {
		mockConsole := mockinput.NewMockConsole()
		mockConsole.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "agent queue")
		}).RespondFn(func(_ input.ConsoleOptions) (any, error) {
			return 0, fmt.Errorf("user cancelled")
		})

		name1 := "Default"
		id1 := 1
		name2 := "Azure Pipelines"
		id2 := 2
		queues := []taskagent.TaskAgentQueue{
			{Id: &id1, Name: &name1},
			{Id: &id2, Name: &name2},
		}

		queue, err := selectAgentQueue(t.Context(), "project-1", queues, mockConsole)
		assert.Nil(t, queue)
		assert.ErrorContains(t, err, "selecting agent queue")
		assert.ErrorContains(t, err, "user cancelled")
	})
}
