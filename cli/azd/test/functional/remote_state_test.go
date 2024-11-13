package cli_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

func Test_StorageBlobClient(t *testing.T) {
	runTestWithRemoteState(t, func(storageConfig *storage.AccountConfig) {
		t.Run("Crud", func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			session := recording.Start(t)

			// Use the session proxy client when recording or in playback
			// Use default http client for live mode
			httpClient := http.DefaultClient
			if session != nil {
				httpClient = session.ProxyClient
			}

			blobClient := createBlobClient(t, mockContext, storageConfig, httpClient)

			blobPath := "test-env/.env"
			envValues := "KEY=VALUE\n"

			// Upload
			reader := bytes.NewBuffer([]byte(envValues))
			err := blobClient.Upload(*mockContext.Context, blobPath, reader)
			require.NoError(t, err)

			// Download
			downloadReader, err := blobClient.Download(*mockContext.Context, blobPath)
			require.NoError(t, err)
			require.NotNil(t, downloadReader)

			downloadBytes, err := io.ReadAll(downloadReader)
			require.NoError(t, err)
			require.Equal(t, envValues, string(downloadBytes))

			// List
			blobs, err := blobClient.Items(*mockContext.Context)
			require.NoError(t, err)
			require.NotEmpty(t, blobs)

			// Delete
			err = blobClient.Delete(*mockContext.Context, blobPath)
			require.NoError(t, err)
		})
	})
}

func createBlobClient(
	t *testing.T,
	mockContext *mocks.MockContext,
	storageConfig *storage.AccountConfig,
	httpClient auth.HttpClient,
) storage.BlobClient {
	coreClientOptions := &azcore.ClientOptions{
		Transport: httpClient,
	}

	fileConfigManager := config.NewFileConfigManager(config.NewManager())

	authManager, err := auth.NewManager(
		fileConfigManager,
		config.NewUserConfigManager(fileConfigManager),
		cloud.AzurePublic(),
		httpClient, mockContext.Console,
		auth.ExternalAuthConfiguration{},
	)
	require.NoError(t, err)

	sdkClient, err := storage.NewBlobSdkClient(
		auth.NewMultiTenantCredentialProvider(authManager), storageConfig, coreClientOptions, cloud.AzurePublic())
	require.NoError(t, err)
	require.NotNil(t, sdkClient)

	return storage.NewBlobClient(storageConfig, sdkClient)
}

type remoteStateTestFunc func(storageConfig *storage.AccountConfig)

func runTestWithRemoteState(t *testing.T, testFunc remoteStateTestFunc) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	session := recording.Start(t)
	envName := randomOrStoredEnvName(session)

	cli, env := provisionRemoteStateStorage(t, ctx, envName, session)
	defer cleanupDeployments(ctx, t, cli, session, envName)
	defer destroyRemoteStateStorage(t, ctx, cli)

	accountConfig := &storage.AccountConfig{
		AccountName:   env.Getenv("AZURE_STORAGE_ACCOUNT_NAME"),
		ContainerName: "azdtest",
	}

	if session != nil {
		session.Variables[recording.SubscriptionIdKey] = env.GetSubscriptionId()
	}

	testFunc(accountConfig)
}

func provisionRemoteStateStorage(
	t *testing.T,
	ctx context.Context,
	envName string,
	session *recording.Session,
) (*azdcli.CLI, *environment.Environment) {
	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	t.Logf("AZURE_ENV_NAME: %s", envName)

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)
	cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

	err := copySample(dir, "azdremotestate")
	require.NoError(t, err, "failed expanding sample")

	_, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
	require.NoError(t, err)

	t.Logf("Starting provision\n")
	_, err = cli.RunCommandWithStdIn(ctx, stdinForProvision(), "provision", "--cwd", dir)
	require.NoError(t, err)

	env, err := envFromAzdRoot(ctx, dir, envName)
	require.NoError(t, err)

	return cli, env
}

func destroyRemoteStateStorage(t *testing.T, ctx context.Context, cli *azdcli.CLI) {
	_, err := cli.RunCommand(ctx, "down", "--force", "--purge")
	require.NoError(t, err)
}
