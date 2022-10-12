// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package funcapp_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/test/functional"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/sethvargo/go-retry"
	"github.com/stretchr/testify/require"
)

func Test_CLI_InfraCreateAndDeleteFuncApp(t *testing.T) {
	t.Skip("azure-dev/834")
	t.Setenv("AZURE_LOCATION", "eastus2")

	ctx, cancel := functional.NewTestContext(t)
	defer cancel()

	dir := functional.TempDirWithDiagnostics(t)
	ostest.Chdir(t, dir)

	err := functional.CopySample(dir, "funcapp")
	require.NoError(t, err, "failed expanding sample")

	_, initResp := functional.NewRandomNameEnvAndInitResponse(t)

	_, _, err = functional.RunCliCommandWithStdIn(t, ctx, initResp, "init")
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "infra", "create")
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "deploy")
	require.NoError(t, err)

	out, _, err := functional.RunCliCommand(t, ctx, "env", "get-values", "-o", "json", "--cwd", dir)
	require.NoError(t, err)

	t.Logf("env get-values command output: %s\n", out)

	var envValues map[string]interface{}
	err = json.Unmarshal([]byte(out), &envValues)
	require.NoError(t, err)

	url := fmt.Sprintf("%s/api/httptrigger", envValues["AZURE_FUNCTION_URI"])

	t.Logf("Issuing GET request to function")

	// We've seen some cases in CI where issuing a get right after a deploy ends up with us getting a 404, so retry
	// the request a handful of times if it fails with a 404.
	err = retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(5*time.Second)), func(ctx context.Context) error {
		res, err := http.Get(url)
		if err != nil {
			return retry.RetryableError(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return retry.RetryableError(fmt.Errorf("expected %d but got %d for request to %s", http.StatusOK, res.StatusCode, url))
		}
		return nil
	})
	require.NoError(t, err)

	_, _, err = functional.RunCliCommand(t, ctx, "infra", "delete", "--force", "--purge")
	require.NoError(t, err)
}

func TestMain(m *testing.M) {
	flag.Parse()
	shortFlag := flag.Lookup("test.short")
	if shortFlag != nil && shortFlag.Value.String() == "true" {
		log.Println("Skipping tests in short mode")
		os.Exit(0)
	}

	exitVal := m.Run()
	os.Exit(exitVal)
}
