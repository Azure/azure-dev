package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// AccountConfig contains the configuration for connecting to a storage account
type AccountConfig struct {
	AccountName   string
	ContainerName string
	Endpoint      string
}

const (
	DefaultBlobEndpoint = "blob.core.windows.net"
)

var (
	ErrContainerNotFound = errors.New("container not found")
)

type BlobClient interface {
	// Download downloads a blob from the configured storage account container.
	Download(ctx context.Context, blobPath string) (io.ReadCloser, error)

	// Upload uploads a blob to the configured storage account container.
	Upload(ctx context.Context, blobPath string, reader io.Reader) error

	// Delete deletes a blob from the configured storage account container.
	Delete(ctx context.Context, blobPath string) error

	// Items returns a list of blobs in the configured storage account container.
	Items(ctx context.Context) ([]*Blob, error)
}

// NewBlobClient creates a new BlobClient instance to manage blobs within a container.
func NewBlobClient(
	config AccountConfig,
	authManager auth.CurrentUserAuthManager,
	httpClient httputil.HttpClient,
) BlobClient {
	return &blobClient{
		config:      config,
		authManager: authManager,
		httpClient:  httpClient,
		userAgent:   internal.UserAgent(),
	}
}

type blobClient struct {
	config             AccountConfig
	authManager        auth.CurrentUserAuthManager
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	userAgent          string
	client             *azblob.Client
}

type Blob struct {
	Name         string
	Path         string
	CreationTime time.Time
	LastModified time.Time
}

func (bc *blobClient) Items(ctx context.Context) ([]*Blob, error) {
	client, err := bc.createClient(ctx)
	if err != nil {
		return nil, err
	}

	blobs := []*Blob{}

	pager := client.NewListBlobsFlatPager(bc.config.ContainerName, nil)
	for pager.More() {
		page, err := pager.NextPage(context.Background())
		if err != nil {
			err = fmt.Errorf("failed to get next page of blobs, %w", err)
			var responseErr *azcore.ResponseError
			if errors.As(err, &responseErr) {
				return nil, fmt.Errorf("%w, %w", err, ErrContainerNotFound)
			}
			return nil, err
		}

		for _, blob := range page.Segment.BlobItems {
			blobs = append(blobs, &Blob{
				Name:         filepath.Base(*blob.Name),
				Path:         *blob.Name,
				CreationTime: *blob.Properties.CreationTime,
				LastModified: *blob.Properties.LastModified,
			})
		}
	}

	return blobs, nil
}

func (bc *blobClient) Download(ctx context.Context, blobPath string) (io.ReadCloser, error) {
	client, err := bc.createClient(ctx)
	if err != nil {
		return nil, err
	}

	if err := bc.ensureContainerExists(ctx); err != nil {
		return nil, err
	}

	resp, err := client.DownloadStream(ctx, bc.config.ContainerName, blobPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download blob '%s', %w", blobPath, err)
	}

	return resp.Body, nil
}

func (bc *blobClient) Upload(ctx context.Context, blobPath string, reader io.Reader) error {
	client, err := bc.createClient(ctx)
	if err != nil {
		return err
	}

	if err := bc.ensureContainerExists(ctx); err != nil {
		return err
	}

	_, err = client.UploadStream(ctx, bc.config.ContainerName, blobPath, reader, nil)
	if err != nil {
		return fmt.Errorf("failed to upload blob '%s', %w", blobPath, err)
	}

	return nil
}

func (bc *blobClient) Delete(ctx context.Context, blobPath string) error {
	client, err := bc.createClient(ctx)
	if err != nil {
		return err
	}

	if err := bc.ensureContainerExists(ctx); err != nil {
		return err
	}

	_, err = client.DeleteBlob(ctx, bc.config.ContainerName, blobPath, nil)
	if err != nil {
		return fmt.Errorf("failed to delete blob '%s', %w", blobPath, err)
	}

	return nil
}

// Check if the specified container exists
// If it doesn't already exist then create it
func (bc *blobClient) ensureContainerExists(ctx context.Context) error {
	client, err := bc.createClient(ctx)
	if err != nil {
		return err
	}

	exists := false

	pager := client.NewListContainersPager(nil)
	for pager.More() {
		if exists {
			break
		}

		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed getting next page of containers: %w", err)
		}

		for _, container := range page.ContainerItems {
			if *container.Name == bc.config.ContainerName {
				exists = true
				break
			}
		}
	}

	if !exists {
		_, err = client.CreateContainer(ctx, bc.config.ContainerName, nil)
		if err != nil {
			return fmt.Errorf("failed to create container '%s', %w", bc.config.ContainerName, err)
		}
	}

	return nil
}

// createClient creates a new blob client and caches it for future use
func (bc *blobClient) createClient(ctx context.Context) (*azblob.Client, error) {
	if bc.client != nil {
		return bc.client, nil
	}

	credential, err := bc.authManager.CredentialForCurrentUser(ctx, nil)
	if err != nil {
		return nil, err
	}

	coreOptions := azsdk.
		DefaultClientOptionsBuilder(ctx, bc.httpClient, bc.userAgent).
		BuildCoreClientOptions()

	blobOptions := &azblob.ClientOptions{
		ClientOptions: *coreOptions,
	}

	if bc.config.Endpoint == "" {
		bc.config.Endpoint = DefaultBlobEndpoint
	}

	serviceUrl := fmt.Sprintf("https://%s.%s", bc.config.AccountName, bc.config.Endpoint)
	client, err := azblob.NewClient(serviceUrl, credential, blobOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create blob client, %w", err)
	}

	bc.client = client

	return bc.client, nil
}
