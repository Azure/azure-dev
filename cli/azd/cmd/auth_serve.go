package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

// TokenResponse defines the structure of the token response.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresOn   int64  `json:"expires_on"`
}

// tokenHandler handles token requests.
func (serve *serveAction) tokenHandler(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	if resource == "" {
		resource = "https://management.azure.com/"
	}

	fmt.Printf("Received request for resource: %s\n", resource)

	ctx := context.Background()
	var cred azcore.TokenCredential

	cred, err := serve.credentialProvider(ctx, &auth.CredentialForCurrentUserOptions{
		NoPrompt: true,
		TenantID: "",
	})
	if err != nil {
		fmt.Printf("credentialProvider: %v", err)
		return
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{resource + "/.default"},
	})
	if err != nil {
		fmt.Printf("fetching token: %v", err)
		return
	}

	res := TokenResponse{
		AccessToken: token.Token,
		ExpiresOn:   token.ExpiresOn.Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(res); err != nil {
		http.Error(w, "Failed to encode response: "+err.Error(), http.StatusInternalServerError)
	}
}

func (serve *serveAction) start(port string) {
	http.HandleFunc("/MSI/token", serve.tokenHandler)
	http.HandleFunc("/metadata/identity/oauth2/token", serve.tokenHandler)

	srv := &http.Server{
		Addr:         ":" + port,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}

	go func() {
		fmt.Printf("Server started on port %s\n", port)
		fmt.Printf("MSI endpoint for local development: http://localhost:%s/MSI/token\n", port)
		fmt.Printf("MSI endpoint for Docker: http://host.docker.internal:%s/MSI/token\n", port)
		fmt.Println("Set the MSI_ENDPOINT environment variable to the appropriate URL above.")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Server stopped: %s\n", err)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown failed: %s\n", err)
	}

	log.Println("Shutting down")
	os.Exit(0)
}

func newServeCmd(parent string) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Starts a local Managed Identity endpoint for development purposes.",
		Annotations: map[string]string{
			loginCmdParentAnnotation: parent,
		},
	}
}

type serveAction struct {
	console            input.Console
	credentialProvider CredentialProviderFn
	formatter          output.Formatter
	writer             io.Writer
}

func newServeAction(
	console input.Console,
	credentialProvider CredentialProviderFn,
	formatter output.Formatter,
	writer io.Writer) actions.Action {
	return &serveAction{
		console:            console,
		credentialProvider: credentialProvider,
		formatter:          formatter,
		writer:             writer,
	}
}

func (serve *serveAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	port := os.Getenv("AZD_AUTH_SERVER_PORT")
	if port == "" {
		port = "53028"
	}
	serve.start(port)
	return nil, nil
}
