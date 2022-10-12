package identity

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

type contextKey string

const credentialsContextKey contextKey = "azure_credentials"

// Gets credentials for Azure from current context
// If not found returns DefaultAzureCredential
func GetCredentials(ctx context.Context) (azcore.TokenCredential, error) {
	cred, ok := ctx.Value(credentialsContextKey).(azcore.TokenCredential)

	if !ok {
		defaultCreds, err := azidentity.NewAzureCLICredential(nil)
		if err != nil {
			return nil, fmt.Errorf("getting azure credentials: %w", err)
		}

		cred = defaultCreds
	}

	return cred, nil
}

// Sets the specified Azure token credential in context and returns new context
func WithCredentials(ctx context.Context, cred azcore.TokenCredential) context.Context {
	return context.WithValue(ctx, credentialsContextKey, cred)
}
