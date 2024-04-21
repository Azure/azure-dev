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
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
)

// AccountConfig contains the configuration for connecting to a storage account
type AccountConfig struct {
	AccountName   string
	ContainerName string
	Endpoint      string
}

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
	config *AccountConfig,
	client *azblob.Client,
) BlobClient {
	return &blobClient{
		config: config,
		client: client,
	}
}

type blobClient struct {
	config *AccountConfig
	client *azblob.Client
}

// Blob represents a blob within a storage account container.
type Blob struct {
	Name         string
	Path         string
	CreationTime time.Time
	LastModified time.Time
}

// Items returns a list of blobs in the configured storage account container.
func (bc *blobClient) Items(ctx context.Context) ([]*Blob, error) {
	if err := bc.ensureContainerExists(ctx); err != nil {
		return nil, err
	}

	blobs := []*Blob{}

	pager := bc.client.NewListBlobsFlatPager(bc.config.ContainerName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get next page of blobs, %w", err)
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

// Download downloads a blob from the configured storage account container.
func (bc *blobClient) Download(ctx context.Context, blobPath string) (io.ReadCloser, error) {
	if err := bc.ensureContainerExists(ctx); err != nil {
		return nil, err
	}

	resp, err := bc.client.DownloadStream(ctx, bc.config.ContainerName, blobPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download blob '%s', %w", blobPath, err)
	}

	return resp.Body, nil
}

// Upload uploads a blob to the configured storage account container.
func (bc *blobClient) Upload(ctx context.Context, blobPath string, reader io.Reader) error {
	if err := bc.ensureContainerExists(ctx); err != nil {
		return err
	}

	_, err := bc.client.UploadStream(ctx, bc.config.ContainerName, blobPath, reader, nil)
	if err != nil {
		return fmt.Errorf("failed to upload blob '%s', %w", blobPath, err)
	}

	return nil
}

// Delete deletes a blob from the configured storage account container.
func (bc *blobClient) Delete(ctx context.Context, blobPath string) error {
	if err := bc.ensureContainerExists(ctx); err != nil {
		return err
	}

	_, err := bc.client.DeleteBlob(ctx, bc.config.ContainerName, blobPath, nil)
	if err != nil {
		return fmt.Errorf("failed to delete blob '%s', %w", blobPath, err)
	}

	return nil
}

// Check if the specified container exists
// If it doesn't already exist then create it
func (bc *blobClient) ensureContainerExists(ctx context.Context) error {
	exists := false

	pager := bc.client.NewListContainersPager(nil)
	for pager.More() {
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
		_, err := bc.client.CreateContainer(ctx, bc.config.ContainerName, nil)
		if err != nil {
			return fmt.Errorf("failed to create container '%s', %w", bc.config.ContainerName, err)
		}
	}

	return nil
}

// createClient creates a new blob client and caches it for future use
func NewBlobSdkClient(
	credentialProvider auth.MultiTenantCredentialProvider,
	accountConfig *AccountConfig,
	coreClientOptions *azcore.ClientOptions,
	cloud *cloud.Cloud,
) (*azblob.Client, error) {
	blobOptions := &azblob.ClientOptions{
		ClientOptions: *coreClientOptions,
	}

	if accountConfig.Endpoint == "" {
		accountConfig.Endpoint = cloud.StorageEndpointSuffix
	}

	// Use home tenant ID
	credential, err := credentialProvider.GetTokenCredential(context.Background(), "")
	if err != nil {
		return nil, err
	}

	serviceUrl := fmt.Sprintf("https://%s.blob.%s", accountConfig.AccountName, accountConfig.Endpoint)
	client, err := azblob.NewClient(serviceUrl, credential, blobOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create blob client, %w", err)
	}

	return client, nil
}
