package mockarmresources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
)

func IsListSubscriptions(request *http.Request) bool {
	return request.Method == http.MethodGet && request.URL.Path == "/subscriptions"
}

func MockListSubscriptions(mockHttp *mockhttp.MockHttpClient, response armsubscriptions.SubscriptionListResult) {
	mockHttp.When(IsListSubscriptions).RespondFn(func(request *http.Request) (*http.Response, error) {
		res := armsubscriptions.ClientListResponse{
			SubscriptionListResult: response,
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

func IsGetSubscription(request *http.Request, subscription string) bool {
	return request.Method == http.MethodGet && request.URL.Path == fmt.Sprintf("/subscriptions/%s", subscription)
}

func MockGetSubscription(mockHttp *mockhttp.MockHttpClient, subscription string, response armsubscriptions.Subscription) {
	mockHttp.When(func(request *http.Request) bool {
		return IsGetSubscription(request, subscription)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		res := armsubscriptions.ClientGetResponse{
			Subscription: response,
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

func IsListTenants(request *http.Request) bool {
	return request.Method == http.MethodGet && request.URL.Path == "/tenants"
}

func MockListTenants(mockHttp *mockhttp.MockHttpClient, response armsubscriptions.TenantListResult) {
	mockHttp.When(IsListTenants).RespondFn(func(request *http.Request) (*http.Response, error) {
		res := armsubscriptions.TenantsClientListResponse{
			TenantListResult: response,
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

func IsListLocations(request *http.Request, subscription string) bool {
	if subscription == "" {
		return request.Method == http.MethodGet &&
			strings.HasPrefix(request.URL.Path, "/subscriptions/") &&
			strings.HasSuffix(request.URL.Path, "/locations")
	}

	return request.Method == http.MethodGet &&
		request.URL.Path == fmt.Sprintf("/subscriptions/%s/locations", subscription)
}

func MockListLocations(
	mockHttp *mockhttp.MockHttpClient,
	subscription string,
	response armsubscriptions.LocationListResult) {
	mockHttp.When(func(request *http.Request) bool {
		return IsListLocations(request, subscription)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		res := armsubscriptions.ClientListLocationsResponse{
			LocationListResult: response,
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
