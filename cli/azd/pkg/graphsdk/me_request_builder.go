package graphsdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// A Microsoft Graph User entity.
type User struct {
	Id                string   `json:"id"`
	DisplayName       string   `json:"displayName"`
	GivenName         string   `json:"givenName"`
	Surname           string   `json:"surname"`
	JobTitle          string   `json:"jobTitle"`
	Mail              string   `json:"mail"`
	OfficeLocation    string   `json:"officeLocation"`
	UserPrincipalName string   `json:"userPrincipalName"`
	BusinessPhones    []string `json:"businessPhones"`
}

type MeItemRequestBuilder struct {
	*EntityItemRequestBuilder[MeItemRequestBuilder]
}

func newMeItemRequestBuilder(client *GraphClient) *MeItemRequestBuilder {
	builder := &MeItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, client, "me")

	return builder
}

// Gets the user profile information for the current logged in user
func (b *MeItemRequestBuilder) Get(ctx context.Context) (*User, error) {
	req, err := b.createRequest(ctx, http.MethodGet, fmt.Sprintf("%s/me", b.client.host))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := b.client.pipeline.Do(req)
	if err != nil {
		return nil, runtime.NewResponseError(res)
	}

	result, err := azsdk.ReadRawResponse[User](res)
	if err != nil {
		return nil, err
	}

	return result, err
}
