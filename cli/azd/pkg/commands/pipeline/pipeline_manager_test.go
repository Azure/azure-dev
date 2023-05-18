// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func Test_checkRoleAssignments(t *testing.T) {
	// Tests the use case for a brand new service principal
	t.Run("AuthorizedRole", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Do you want to continue with your custom role assignment")
		}).Respond(false)
		mockgraphsdk.RegisterUserRoleAssignment(mockContext, http.StatusOK, "Owner")

		azCli := mockazcli.NewAzCliFromMockContext(mockContext)
		err := checkRoleAssignments(
			*mockContext.Context,
			azCli,
			"SUBSCRIPTION_ID",
			"principal_id",
			console,
		)
		require.NoError(t, err)
	})

	t.Run("UnauthorizedRole", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Do you have a custom role assignment with such access")
		}).Respond(false)
		mockgraphsdk.RegisterUserRoleAssignment(mockContext, http.StatusOK, "Contributor")

		azCli := mockazcli.NewAzCliFromMockContext(mockContext)
		err := checkRoleAssignments(
			*mockContext.Context,
			azCli,
			"SUBSCRIPTION_ID",
			"principal_id",
			console,
		)

		require.Error(t, err)
	})
}
