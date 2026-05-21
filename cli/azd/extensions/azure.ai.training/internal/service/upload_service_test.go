// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.training/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Fakes ---

type fakeUploadClient struct {
	getResp  *models.DatasetVersion
	getErr   error
	getCalls int

	deleteErr   error
	deleteCalls int

	startResp  *models.PendingUploadResponse
	startErr   error
	startCalls int

	createResp  *models.DatasetVersion
	createErr   error
	createCalls int
	lastCreate  *models.DatasetVersion
}

func (f *fakeUploadClient) GetDatasetVersion(
	_ context.Context, _, _ string,
) (*models.DatasetVersion, error) {
	f.getCalls++
	return f.getResp, f.getErr
}

func (f *fakeUploadClient) DeleteDatasetVersion(_ context.Context, _, _ string) error {
	f.deleteCalls++
	return f.deleteErr
}

func (f *fakeUploadClient) StartPendingUpload(
	_ context.Context, _, _ string,
) (*models.PendingUploadResponse, error) {
	f.startCalls++
	return f.startResp, f.startErr
}

func (f *fakeUploadClient) CreateOrUpdateDatasetVersion(
	_ context.Context, _, _ string, dataset *models.DatasetVersion,
) (*models.DatasetVersion, error) {
	f.createCalls++
	f.lastCreate = dataset
	return f.createResp, f.createErr
}

type fakeUploadRunner struct {
	err     error
	calls   int
	lastSrc string
	lastSAS string
}

func (f *fakeUploadRunner) Copy(_ context.Context, src, sasURI string) error {
	f.calls++
	f.lastSrc = src
	f.lastSAS = sasURI
	return f.err
}

// makeTempDir creates a small temp directory with a single file so we get a
// stable, non-empty content hash to work with.
func makeTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("payload"), 0o600))
	return dir
}

// validPendingUploadResponse returns a minimal PendingUploadResponse where
// BlobReference + SASUri are present (required by doUpload).
func validPendingUploadResponse() *models.PendingUploadResponse {
	return &models.PendingUploadResponse{
		BlobReference: &models.BlobReference{
			BlobURI: "https://storage.blob.core.windows.net/c/datasets/x/v1",
			Credential: models.SASCredential{
				SASUri:         "https://storage.blob.core.windows.net/c/datasets/x/v1?sig=abc",
				CredentialType: "SAS",
			},
		},
	}
}

// --- Tests ---

func TestUploadDirectory_DedupHit_SkipsUpload(t *testing.T) {
	dir := makeTempDir(t)
	expectedHash, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)

	client := &fakeUploadClient{
		getResp: &models.DatasetVersion{
			ID:   "/datasets/x/versions/abc",
			Tags: map[string]string{"contentHash": expectedHash},
		},
	}
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	res, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.NoError(t, err)

	assert.True(t, res.Skipped, "expected Skipped=true on sentinel match")
	assert.False(t, res.Collision)
	assert.Equal(t, "/datasets/x/versions/abc", res.DatasetResourceID)
	assert.Equal(t, 1, client.getCalls)
	assert.Equal(t, 0, client.startCalls, "must not start a new upload when dedup hits")
	assert.Equal(t, 0, runner.calls)
	assert.Equal(t, 0, client.createCalls)
}

func TestUploadDirectory_ZombieRecovery_DeletesAndReuploads(t *testing.T) {
	dir := makeTempDir(t)
	fullHash, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)

	client := &fakeUploadClient{
		// Existing version with no contentHash tag → zombie
		getResp: &models.DatasetVersion{
			ID:   "/datasets/x/versions/zombie",
			Tags: map[string]string{},
		},
		startResp:  validPendingUploadResponse(),
		createResp: &models.DatasetVersion{ID: "/datasets/x/versions/new"},
	}
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	res, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.NoError(t, err)

	assert.False(t, res.Skipped)
	assert.False(t, res.Collision)
	assert.Equal(t, "/datasets/x/versions/new", res.DatasetResourceID)
	assert.Equal(t, 1, client.deleteCalls, "zombie version must be deleted before re-upload")
	assert.Equal(t, 1, client.startCalls)
	assert.Equal(t, 1, runner.calls)
	assert.Equal(t, 1, client.createCalls)

	// Sentinel tag must be written on the re-upload
	require.NotNil(t, client.lastCreate)
	require.NotNil(t, client.lastCreate.Tags)
	assert.Equal(t, fullHash, client.lastCreate.Tags["contentHash"])
}

func TestUploadDirectory_HashCollision_ReturnsCollisionFlag(t *testing.T) {
	dir := makeTempDir(t)

	// Existing version exists with a *different* contentHash → 49-char prefix
	// collided but full hashes differ. Caller should retry with unique naming.
	client := &fakeUploadClient{
		getResp: &models.DatasetVersion{
			ID:   "/datasets/x/versions/other",
			Tags: map[string]string{"contentHash": "different-full-hash-value"},
		},
	}
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	res, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.NoError(t, err)

	assert.True(t, res.Collision)
	assert.False(t, res.Skipped)
	assert.Equal(t, 0, client.startCalls)
	assert.Equal(t, 0, client.deleteCalls)
	assert.Equal(t, 0, runner.calls)
}

func TestUploadDirectory_NoExistingVersion_FullUploadWithSentinel(t *testing.T) {
	dir := makeTempDir(t)
	fullHash, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)

	client := &fakeUploadClient{
		getResp:    nil, // GET returned 404 → no existing version
		startResp:  validPendingUploadResponse(),
		createResp: &models.DatasetVersion{ID: "/datasets/x/versions/v1"},
	}
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	res, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.NoError(t, err)

	assert.Equal(t, "/datasets/x/versions/v1", res.DatasetResourceID)
	assert.False(t, res.Skipped)
	assert.False(t, res.Collision)
	assert.Equal(t, 1, client.startCalls)
	assert.Equal(t, 1, runner.calls)
	assert.Equal(t, 1, client.createCalls)
	assert.Equal(t, 0, client.deleteCalls)

	require.NotNil(t, client.lastCreate)
	assert.Equal(t, fullHash, client.lastCreate.Tags["contentHash"])
	assert.Equal(t, "uri_folder", client.lastCreate.DataType)
}

func TestUploadDirectory_MissingSASURI_ReturnsError(t *testing.T) {
	dir := makeTempDir(t)
	client := &fakeUploadClient{
		startResp: &models.PendingUploadResponse{
			BlobReference: &models.BlobReference{
				BlobURI: "https://storage.blob.core.windows.net/c/datasets/x/v1",
				// Credential.SASUri intentionally empty
			},
		},
	}
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no SAS URI")
	assert.Equal(t, 0, runner.calls, "azcopy must not be invoked without a SAS URI")
}

func TestUploadDirectory_NilBlobReference_ReturnsError(t *testing.T) {
	dir := makeTempDir(t)
	client := &fakeUploadClient{
		startResp: &models.PendingUploadResponse{BlobReference: nil},
	}
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no SAS URI")
}

func TestUploadDirectory_AzcopyFailure_PropagatesError(t *testing.T) {
	dir := makeTempDir(t)
	client := &fakeUploadClient{
		startResp: validPendingUploadResponse(),
	}
	runner := &fakeUploadRunner{err: errors.New("azcopy exit status 1")}

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to upload files")
	assert.Contains(t, err.Error(), "azcopy exit status 1")
	assert.Equal(t, 0, client.createCalls, "PATCH must not be called when azcopy fails")
}

func TestUploadDirectory_GetError_Propagates(t *testing.T) {
	dir := makeTempDir(t)
	client := &fakeUploadClient{getErr: errors.New("network down")}
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check existing dataset version")
	assert.Equal(t, 0, client.startCalls)
}

func TestUploadDirectory_ZombieDeleteFails_Propagates(t *testing.T) {
	dir := makeTempDir(t)
	client := &fakeUploadClient{
		getResp:   &models.DatasetVersion{ID: "/datasets/x/versions/z", Tags: map[string]string{}},
		deleteErr: errors.New("delete denied"),
	}
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete zombie")
	assert.Equal(t, 0, client.startCalls)
}

func TestUploadDirectory_MissingLocalPath_ReturnsError(t *testing.T) {
	client := &fakeUploadClient{}
	runner := &fakeUploadRunner{}
	svc := &UploadService{client: client, azcopyRunner: runner}

	_, err := svc.UploadDirectory(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"), "x", "desc")
	require.Error(t, err)
	assert.Equal(t, 0, client.getCalls, "must fail before any API call when hashing fails")
}

func TestUploadDirectoryNoDedup_SkipsLookupAndUploadsWithoutTag(t *testing.T) {
	dir := makeTempDir(t)
	client := &fakeUploadClient{
		startResp:  validPendingUploadResponse(),
		createResp: &models.DatasetVersion{ID: "/datasets/x/versions/1"},
	}
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	res, err := svc.UploadDirectoryNoDedup(context.Background(), dir, "x", "1", "desc")
	require.NoError(t, err)

	assert.Equal(t, "/datasets/x/versions/1", res.DatasetResourceID)
	assert.Equal(t, 0, client.getCalls, "no-dedup path must not check existing version")
	assert.Equal(t, 1, client.startCalls)
	assert.Equal(t, 1, runner.calls)
	assert.Equal(t, 1, client.createCalls)
	require.NotNil(t, client.lastCreate)
	assert.Nil(t, client.lastCreate.Tags, "no-dedup path must not write sentinel tag")
}

func TestUploadDirectory_PassesAbsolutePathToAzcopy(t *testing.T) {
	dir := makeTempDir(t)
	client := &fakeUploadClient{
		startResp:  validPendingUploadResponse(),
		createResp: &models.DatasetVersion{ID: "/datasets/x/versions/v1"},
	}
	runner := &fakeUploadRunner{}

	// Use a relative path to verify the service resolves it before invoking azcopy.
	cwd, err := os.Getwd()
	require.NoError(t, err)
	rel, err := filepath.Rel(cwd, dir)
	require.NoError(t, err)

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err = svc.UploadDirectory(context.Background(), rel, "x", "desc")
	require.NoError(t, err)

	assert.True(t, filepath.IsAbs(runner.lastSrc),
		"azcopy must be invoked with an absolute path, got %q", runner.lastSrc)
}

// Compile-time check that fakes satisfy the unexported interfaces.
var (
	_ uploadClient = (*fakeUploadClient)(nil)
	_ uploadRunner = (*fakeUploadRunner)(nil)
)
