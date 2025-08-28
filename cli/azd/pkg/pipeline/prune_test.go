package pipeline

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

type pruneMockEntra struct {
	entraid.EntraIdService
	listed  []graphsdk.FederatedIdentityCredential
	deleted []string
}

func (m *pruneMockEntra) ListFederatedCredentials(
	ctx context.Context,
	sub string,
	client string,
) ([]graphsdk.FederatedIdentityCredential, error) {
	return m.listed, nil
}
func (m *pruneMockEntra) DeleteFederatedCredential(ctx context.Context, sub, client, id string) error {
	m.deleted = append(m.deleted, id)
	return nil
}

// Implement minimal methods used by prune helpers (unused in this test) with no-op behavior.
func (m *pruneMockEntra) GetServicePrincipal(
	ctx context.Context,
	sub string,
	appIdOrName string,
) (*graphsdk.ServicePrincipal, error) {
	return nil, nil
}
func (m *pruneMockEntra) CreateOrUpdateServicePrincipal(
	ctx context.Context,
	sub string,
	appIdOrName string,
	opts entraid.CreateOrUpdateServicePrincipalOptions,
) (*graphsdk.ServicePrincipal, error) {
	return nil, nil
}
func (m *pruneMockEntra) ResetPasswordCredentials(
	ctx context.Context,
	sub string,
	appId string,
) (*entraid.AzureCredentials, error) {
	return nil, nil
}
func (m *pruneMockEntra) ApplyFederatedCredentials(
	ctx context.Context,
	sub string,
	client string,
	creds []*graphsdk.FederatedIdentityCredential,
) ([]*graphsdk.FederatedIdentityCredential, error) {
	return nil, nil
}
func (m *pruneMockEntra) CreateRbac(ctx context.Context, sub, scope, roleId, principalId string) error {
	return nil
}
func (m *pruneMockEntra) EnsureRoleAssignments(
	ctx context.Context,
	sub string,
	roles []string,
	sp *graphsdk.ServicePrincipal,
) error {
	return nil
}

func Test_pruneLegacyFederatedCredentials(t *testing.T) {
	mock := &pruneMockEntra{listed: []graphsdk.FederatedIdentityCredential{
		{Id: ptr("1"), Subject: "repo:owner/repo:pull_request", Issuer: federatedIdentityIssuer},
		{Id: ptr("2"), Subject: "repo:owner/repo:ref:refs/heads/main", Issuer: federatedIdentityIssuer},
		{Id: ptr("3"), Subject: "repo:owner/repo:environment:Dev", Issuer: federatedIdentityIssuer},
		{Id: ptr("4"), Subject: "repo:owner/repo:ref:refs/heads/feature", Issuer: federatedIdentityIssuer},
	}}
	mockCtx := mocks.NewMockContext(context.Background())
	pruneLegacyFederatedCredentials(context.Background(), mockCtx.Console, mock, "sub", "client")
	require.ElementsMatch(t, []string{"1", "2", "4"}, mock.deleted)
}

// helper ptr
func ptr[T any](v T) *T { return &v }

// nilConsole satisfies input.Console for pruning (only MessageUxItem used) without output.
// no custom console needed (using mocks.MockContext Console)
