// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Program tfoidc is a simple adapter that presents a GitHub Actions style OIDC token endpoint backed by an Azure
// DevOps service connection. It can also be used to continuously refresh the Azure CLI federated token, as the
// AzureCLI task would.
//
// It requires the following environment variables to be set:
//
// - AZURESUBSCRIPTION_SERVICE_CONNECTION_ID: the Azure DevOps service connection ID
// - SYSTEM_ACCESSTOKEN: the Azure DevOps system access token
// - SYSTEM_OIDCREQUESTURI: the Azure DevOps OIDC request URI
//
// When the -refresh-az flag is passed, it also requires the following environment variables to be set, which should
// correspond to the configured Azure service connection:
//
// - AZURESUBSCRIPTION_SUBSCRIPTION_ID: the Azure subscription ID
// - AZURESUBSCRIPTION_CLIENT_ID: the Azure service principal client ID
// - AZURESUBSCRIPTION_TENANT_ID: the Azure service principal tenant ID
//
// The adapter listens on http://127.0.0.1:27838 and is secured by using SYSTEM_ACCESSTOKEN as a bearer token.
//
// Configure the Azure TF provider to use this endpoint by setting the following environment variables:
//
// ARM_USE_OIDC=true
// ARM_OIDC_REQUEST_URL=http://localhost:27838/oidctoken
// ARM_OIDC_REQUEST_TOKEN=$(System.AccessToken)
//
// When `-refresh-az` is set, the adapter will refresh the Azure CLI federated token every 8 minutes, calling
// `az login` and `az account set` each time. This is useful for long-running Terraform operations that require
// Azure CLI authentication.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

var refreshAz = flag.Bool("refresh-az", false, "Refresh the Azure CLI federated token")

func main() {
	flag.Parse()

	if os.Getenv("AZURESUBSCRIPTION_SERVICE_CONNECTION_ID") == "" {
		log.Fatal("AZURESUBSCRIPTION_SERVICE_CONNECTION_ID is not set")
	}

	if os.Getenv("SYSTEM_ACCESSTOKEN") == "" {
		log.Fatal("SYSTEM_ACCESSTOKEN is not set")
	}

	if os.Getenv("SYSTEM_OIDCREQUESTURI") == "" {
		log.Fatal("SYSTEM_OIDCREQUESTURI is not set")
	}

	if *refreshAz {
		if os.Getenv("AZURESUBSCRIPTION_SUBSCRIPTION_ID") == "" {
			log.Fatal("AZURESUBSCRIPTION_SUBSCRIPTION_ID is not set")
		}

		if os.Getenv("AZURESUBSCRIPTION_CLIENT_ID") == "" {
			log.Fatal("AZURESUBSCRIPTION_CLIENT_ID is not set")
		}

		if os.Getenv("AZURESUBSCRIPTION_TENANT_ID") == "" {
			log.Fatal("AZURESUBSCRIPTION_TENANT_ID is not set")
		}

		go refreshAzFederatedToken()
	}

	listener, err := net.Listen("tcp", "127.0.0.1:27838")
	if err != nil {
		log.Fatalf("Failed to listen on a port: %v", err)
	}
	defer listener.Close()

	http.HandleFunc("/oidctoken", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+os.Getenv("SYSTEM_ACCESSTOKEN") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		tokenResponse, err := fetchOIDCToken(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Value string `json:"value"`
			Count int    `json:"count"`
		}{tokenResponse, 0})
	})

	log.Fatal(
		// nolint:gosec
		http.Serve(listener, nil),
	)
}

const refreshTimeout = 8 * time.Minute

func refreshAzFederatedToken() {
	_, err := exec.LookPath("az")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tfoidc: az CLI is not installed\n")
		return
	}

	first := true

	for {
		if !first {
			time.Sleep(refreshTimeout)
			first = false
		}

		token, err := fetchOIDCToken(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "tfoidc: failed to fetch OIDC token: %v\n", err)
			continue
		}

		// nolint:gosec
		loginCmd := exec.Command("az",
			"login",
			"--service-principal",
			"--username", os.Getenv("AZURESUBSCRIPTION_CLIENT_ID"),
			"--tenant", os.Getenv("AZURESUBSCRIPTION_TENANT_ID"),
			"--allow-no-subscriptions",
			"--federated-token", token,
			"--output", "none",
		)
		if err := loginCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "tfoidc: failed to run az login: %v\n", err)
			continue
		}

		// nolint:gosec
		subSelectCmd := exec.Command("az",
			"account",
			"set",
			"--subscription", os.Getenv("AZURESUBSCRIPTION_SUBSCRIPTION_ID"),
		)
		if err := subSelectCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "tfoidc: failed to run az account set: %v\n", err)
			continue
		}
	}
}

func fetchOIDCToken(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s?api-version=7.1&serviceConnectionId=%s",
		os.Getenv("SYSTEM_OIDCREQUESTURI"),
		os.Getenv("AZURESUBSCRIPTION_SERVICE_CONNECTION_ID"))
	url, err := runtime.EncodeQueryParams(url)
	if err != nil {
		return "", err
	}

	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}

	tokenReq.Header.Set("Authorization", "Bearer "+os.Getenv("SYSTEM_ACCESSTOKEN"))
	tokenRes, err := http.DefaultClient.Do(tokenReq)
	if err != nil {
		return "", err
	}
	if tokenRes.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s response from the OIDC endpoint. Check service connection ID and Pipeline configuration",
			tokenRes.Status)
	}
	b, err := runtime.Payload(tokenRes)
	if err != nil {
		return "", err
	}
	var tokenResponse struct {
		OIDCToken string `json:"oidcToken"`
	}
	err = json.Unmarshal(b, &tokenResponse)
	if err != nil {
		return "", err
	}

	return tokenResponse.OIDCToken, nil
}
