package mocks

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	mockconsole "github.com/azure/azure-dev/cli/azd/test/mocks/console"
	mockexec "github.com/azure/azure-dev/cli/azd/test/mocks/executil"
	mockhttp "github.com/azure/azure-dev/cli/azd/test/mocks/httputil"
)

type MockContext struct {
	Context    *context.Context
	Console    *mockconsole.MockConsole
	HttpClient *mockhttp.MockHttpClient
	ExecUtil   *mockexec.MockExecUtil
}

func NewMockContext(ctx context.Context) *MockContext {
	mockConsole := mockconsole.NewMockConsole()
	execUtil := mockexec.NewMockExecUtil()
	http := mockhttp.NewMockHttpUtil()

	mockexec.AddAzLoginMocks(execUtil)
	mockhttp.AddDefaultMocks(http)

	ctx = input.WithConsole(ctx, mockConsole)
	ctx = executil.WithExecUtil(ctx, execUtil.RunWithResult)
	ctx = httputil.WithHttpClient(ctx, http)

	mockContext := &MockContext{
		Context:    &ctx,
		Console:    mockConsole,
		ExecUtil:   execUtil,
		HttpClient: http,
	}

	return mockContext
}
