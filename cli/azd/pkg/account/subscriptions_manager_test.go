package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockarmresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionsManager_ListSubscriptions(t *testing.T) {
	type args struct {
		principalInfo      *principalInfoProviderMock
		tenants            []*armsubscriptions.TenantIDDescription
		subscriptions      map[string][]*armsubscriptions.Subscription
		subscriptionErrors map[string]error
	}
	tests := []struct {
		name    string
		args    args
		want    []Subscription
		wantErr bool
	}{
		{
			name: "WhenServicePrincipal",
			args: args{
				principalInfo: &principalInfoProviderMock{
					GetLoggedInServicePrincipalTenantIDFunc: func(context.Context) (*string, error) {
						return convert.RefOf("TENANT_ID_1"), nil
					},
				},
				tenants:       generateTenants(1),
				subscriptions: generateSubscriptions(5, "TENANT_ID_1"),
			},
			want:    toExpectedSubscriptions(generateSubscriptions(5, "TENANT_ID_1")),
			wantErr: false,
		},
		{
			name: "SingleTenant",
			args: args{
				tenants:       generateTenants(1),
				subscriptions: generateSubscriptions(5, "TENANT_ID_1"),
			},
			want:    toExpectedSubscriptions(generateSubscriptions(5, "TENANT_ID_1")),
			wantErr: false,
		},
		{
			name: "LotsOfTenants",
			args: args{
				tenants:       generateTenants(100),
				subscriptions: generateSubscriptionsForTenants(10, 100),
			},
			want:    toExpectedSubscriptions(generateSubscriptionsForTenants(10, 100)),
			wantErr: false,
		},
		{
			name: "NoTenants",
			args: args{
				tenants:       generateTenants(0),
				subscriptions: generateSubscriptions(0, ""),
			},
			want:    []Subscription{},
			wantErr: false,
		},
		{
			name: "NoSubscriptions",
			args: args{
				tenants:       generateTenants(1),
				subscriptions: generateSubscriptions(0, "TENANT_ID_1"),
			},
			want:    []Subscription{},
			wantErr: false,
		},
		{
			name: "SomeTenantErrors",
			args: args{
				tenants:       generateTenants(5),
				subscriptions: generateSubscriptionsForTenants(1, 5),
				subscriptionErrors: map[string]error{
					"TENANT_ID_2": fmt.Errorf("invalid"),
					"TENANT_ID_3": fmt.Errorf("AADSTS50076"),
				},
			},
			want:    toExpectedSubscriptions(generateSubscriptions(1, "TENANT_ID_1", "TENANT_ID_4", "TENANT_ID_5")),
			wantErr: false,
		},
		{
			name: "AllTenantErrors",
			args: args{
				tenants:       generateTenants(3),
				subscriptions: generateSubscriptionsForTenants(1, 3),
				subscriptionErrors: map[string]error{
					"TENANT_ID_1": fmt.Errorf("unknownError"),
					"TENANT_ID_2": fmt.Errorf("invalid"),
					"TENANT_ID_3": fmt.Errorf("AADSTS50076"),
				},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			mockHttp := mockhttp.NewMockHttpUtil()
			mockarmresources.MockListTenants(mockHttp, armsubscriptions.TenantListResult{
				Value: tt.args.tenants,
			})

			for tenant, subs := range tt.args.subscriptions {
				tenantID := tenant
				subs := subs
				whenTenantRequest := mockHttp.When(func(request *http.Request) bool {
					return mockarmresources.IsListSubscriptions(request) && mockhttp.HasBearerToken(request, tenantID)
				})

				// If error is registered, use the error
				if err, ok := tt.args.subscriptionErrors[tenant]; ok {
					whenTenantRequest.SetNonRetriableError(err)
					continue
				}

				whenTenantRequest.RespondFn(func(request *http.Request) (*http.Response, error) {
					res := armsubscriptions.ClientListResponse{
						SubscriptionListResult: armsubscriptions.SubscriptionListResult{
							Value: subs,
						},
					}

					jsonBytes, _ := json.Marshal(res)

					return &http.Response{
						Request:    request,
						StatusCode: http.StatusOK,
						Header:     http.Header{},
						Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
					}, nil
				})
			}

			principalInfo := &principalInfoProviderMock{}
			if tt.args.principalInfo != nil {
				principalInfo = tt.args.principalInfo
			}

			subManager := &SubscriptionsManager{
				service: NewSubscriptionsService(
					&mocks.MockMultiTenantCredentialProvider{},
					armClientOptions(mockHttp),
				),
				cache:         NewBypassSubscriptionsCache(),
				principalInfo: principalInfo,
				console:       mockinput.NewMockConsole(),
			}

			got, err := subManager.ListSubscriptions(ctx)
			if tt.wantErr {
				require.Error(t, err)
			}

			require.Equal(t, tt.want, got)
		})
	}
}

func generateTenants(total int) []*armsubscriptions.TenantIDDescription {
	results := make([]*armsubscriptions.TenantIDDescription, 0, total)
	for i := 1; i <= total; i++ {
		results = append(results, &armsubscriptions.TenantIDDescription{
			DisplayName:   convert.RefOf(fmt.Sprintf("TENANT_%d", i)),
			TenantID:      convert.RefOf(fmt.Sprintf("TENANT_ID_%d", i)),
			DefaultDomain: convert.RefOf(fmt.Sprintf("TENANT_DOMAIN_%d", i)),
		})
	}
	return results
}

func generateSubscriptionsForTenants(total int, totalTenants int) map[string][]*armsubscriptions.Subscription {
	results := make(map[string][]*armsubscriptions.Subscription, totalTenants)
	for i := 1; i <= totalTenants; i++ {
		tenantID := fmt.Sprintf("TENANT_ID_%d", i)
		results[tenantID] = generateSubscriptions(total, tenantID)[tenantID]
	}
	return results
}

func generateSubscriptions(total int, tenantIDs ...string) map[string][]*armsubscriptions.Subscription {
	results := make(map[string][]*armsubscriptions.Subscription, len(tenantIDs))

	for _, tenantID := range tenantIDs {
		tenantID := tenantID
		subs := make([]*armsubscriptions.Subscription, 0, total)
		for i := 1; i <= total; i++ {
			subs = append(subs, &armsubscriptions.Subscription{
				ID:             convert.RefOf(fmt.Sprintf("subscriptions/SUBSCRIPTION_%d", i)),
				SubscriptionID: convert.RefOf(fmt.Sprintf("SUBSCRIPTION_%d_%s", i, tenantID)),
				DisplayName:    convert.RefOf(fmt.Sprintf("Subscription %d (%s)", i, tenantID)),
				TenantID:       &tenantID,
			})
		}
		results[tenantID] = subs
	}

	return results
}

// Converts the raw ARM subscription response object into an account subscription.
// Also sorts names since this is the expected behavior.
func toExpectedSubscriptions(armTenantSubs map[string][]*armsubscriptions.Subscription) []Subscription {
	results := []Subscription{}
	for _, tenantSubs := range armTenantSubs {
		for _, armSub := range tenantSubs {
			results = append(results, Subscription{
				Id:                 *armSub.SubscriptionID,
				Name:               *armSub.DisplayName,
				TenantId:           *armSub.TenantID,
				UserAccessTenantId: *armSub.TenantID,
				IsDefault:          false,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	return results
}
