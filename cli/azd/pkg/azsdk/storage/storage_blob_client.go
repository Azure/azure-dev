// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
)

// AccountConfig contains the configuration for connecting to a storage account
type AccountConfig struct {
	AccountName    string
	ContainerName  string
	Endpoint       string
	SubscriptionId string
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
	config            *AccountConfig
	client            *azblob.Client
	containerVerified bool
	mu                sync.Mutex
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
	if err := bc.ensureContainerReady(ctx); err != nil {
		return nil, err
	}

	blobs, err := bc.listBlobs(ctx)
	if err != nil {
		if bc.isContainerNotFound(err) {
			if createErr := bc.resetAndEnsureContainer(ctx); createErr != nil {
				return nil, createErr
			}
			return bc.listBlobs(ctx)
		}
		return nil, err
	}

	return blobs, nil
}

func (bc *blobClient) listBlobs(ctx context.Context) ([]*Blob, error) {
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
	if err := bc.ensureContainerReady(ctx); err != nil {
		return nil, err
	}

	resp, err := bc.client.DownloadStream(ctx, bc.config.ContainerName, blobPath, nil)
	if err != nil {
		if bc.isContainerNotFound(err) {
			if createErr := bc.resetAndEnsureContainer(ctx); createErr != nil {
				return nil, createErr
			}
			resp, err = bc.client.DownloadStream(ctx, bc.config.ContainerName, blobPath, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to download blob '%s', %w", blobPath, err)
			}
			return resp.Body, nil
		}
		return nil, fmt.Errorf("failed to download blob '%s', %w", blobPath, err)
	}

	return resp.Body, nil
}

// Upload uploads a blob to the configured storage account container.
func (bc *blobClient) Upload(ctx context.Context, blobPath string, reader io.Reader) error {
	if err := bc.ensureContainerReady(ctx); err != nil {
		return err
	}

	_, err := bc.client.UploadStream(ctx, bc.config.ContainerName, blobPath, reader, nil)
	if err != nil {
		if bc.isContainerNotFound(err) {
			if createErr := bc.resetAndEnsureContainer(ctx); createErr != nil {
				return createErr
			}
			// Only retry if the reader supports seeking back to the start.
			// io.Reader is non-rewindable, so retrying with an exhausted
			// reader would upload empty/partial content.
			if seeker, ok := reader.(io.Seeker); ok {
				if _, seekErr := seeker.Seek(0, io.SeekStart); seekErr == nil {
					_, err = bc.client.UploadStream(
						ctx, bc.config.ContainerName, blobPath, reader, nil)
					if err != nil {
						return fmt.Errorf(
							"failed to upload blob '%s', %w", blobPath, err)
					}
					return nil
				}
			}
			// Container re-created but can't retry upload; caller must retry
			return fmt.Errorf("failed to upload blob '%s', %w", blobPath, err)
		}
		return fmt.Errorf("failed to upload blob '%s', %w", blobPath, err)
	}

	return nil
}

// Delete deletes a blob from the configured storage account container.
func (bc *blobClient) Delete(ctx context.Context, blobPath string) error {
	if err := bc.ensureContainerReady(ctx); err != nil {
		return err
	}

	_, err := bc.client.DeleteBlob(ctx, bc.config.ContainerName, blobPath, nil)
	if err != nil {
		if bc.isContainerNotFound(err) {
			if createErr := bc.resetAndEnsureContainer(ctx); createErr != nil {
				return createErr
			}
			_, err = bc.client.DeleteBlob(ctx, bc.config.ContainerName, blobPath, nil)
			if err != nil {
				return fmt.Errorf("failed to delete blob '%s', %w", blobPath, err)
			}
			return nil
		}
		return fmt.Errorf("failed to delete blob '%s', %w", blobPath, err)
	}

	return nil
}

// ensureContainerReady checks that the container exists on the first call,
// then skips the check on subsequent calls. If a container-not-found error
// occurs during an operation, callers use resetAndEnsureContainer to recover.
func (bc *blobClient) ensureContainerReady(ctx context.Context) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.containerVerified {
		return nil
	}

	if err := bc.ensureContainerExists(ctx); err != nil {
		return err
	}

	bc.containerVerified = true
	return nil
}

// resetAndEnsureContainer resets the verified flag and re-checks/creates the container.
// Used when an operation fails with container-not-found (e.g., container deleted externally).
func (bc *blobClient) resetAndEnsureContainer(ctx context.Context) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	bc.containerVerified = false

	if err := bc.ensureContainerExists(ctx); err != nil {
		return err
	}

	bc.containerVerified = true
	return nil
}

// isContainerNotFound checks if the error indicates the container was not found.
func (bc *blobClient) isContainerNotFound(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.ErrorCode == "ContainerNotFound"
	}
	return false
}

// ensureContainerExists checks if the container exists and creates it if not.
func (bc *blobClient) ensureContainerExists(ctx context.Context) error {
	exists := false

	pager := bc.client.NewListContainersPager(nil)
	for pager.More() && !exists {
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
	tenantResolver account.SubscriptionTenantResolver,
) (*azblob.Client, error) {
	blobOptions := &azblob.ClientOptions{
		ClientOptions: *coreClientOptions,
	}

	if accountConfig.Endpoint == "" {
		accountConfig.Endpoint = cloud.StorageEndpointSuffix
	}

	// Determine which tenant to use for authentication
	tenantId := ""
	if accountConfig.SubscriptionId != "" {
		// If a subscription ID is configured, resolve the tenant ID for that subscription
		resolvedTenantId, err := tenantResolver.LookupTenant(context.Background(), accountConfig.SubscriptionId)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to resolve tenant for subscription '%s': %w", accountConfig.SubscriptionId, err)
		}
		tenantId = resolvedTenantId
	}
	// Otherwise, use home tenant ID (empty string)

	credential, err := credentialProvider.GetTokenCredential(context.Background(), tenantId)
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
