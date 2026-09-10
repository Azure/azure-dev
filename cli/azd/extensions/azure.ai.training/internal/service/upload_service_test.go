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
	trainingmocks "azure.ai.training/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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

func newMockUploadClient(t *testing.T) *trainingmocks.UploadClient {
	t.Helper()
	client := &trainingmocks.UploadClient{}
	client.Test(t)
	t.Cleanup(func() {
		client.AssertExpectations(t)
	})
	return client
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

	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return(&models.DatasetVersion{
		ID:   "/datasets/x/versions/abc",
		Tags: map[string]string{"contentHash": expectedHash},
	}, nil).Once()
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	res, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.NoError(t, err)

	assert.True(t, res.Skipped, "expected Skipped=true on sentinel match")
	assert.False(t, res.Collision)
	assert.Equal(t, "/datasets/x/versions/abc", res.DatasetResourceID)
	assert.Equal(t, 0, runner.calls)
}

func TestUploadDirectory_StorageConnectionDoesNotReuseDefaultStorageVersion(t *testing.T) {
	dir := makeTempDir(t)
	fullHash, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)
	defaultVersion := TruncateHashVersion(fullHash)

	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.MatchedBy(func(version string) bool {
			return version != defaultVersion
		}),
	).Return((*models.DatasetVersion)(nil), nil).Once()
	client.On(
		"StartPendingUpload",
		mock.Anything,
		"x",
		mock.Anything,
		"project-storage",
	).Return(validPendingUploadResponse(), nil).Once()
	client.On(
		"CreateOrUpdateDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
		mock.AnythingOfType("*models.DatasetVersion"),
	).Return(&models.DatasetVersion{ID: "/datasets/x/versions/byos"}, nil).Once()
	runner := &fakeUploadRunner{}
	svc := &UploadService{
		client:                client,
		azcopyRunner:          runner,
		storageConnectionName: "project-storage",
	}

	result, err := svc.UploadDirectory(t.Context(), dir, "x", "desc")
	require.NoError(t, err)

	assert.False(t, result.Skipped)
	assert.NotEqual(t, defaultVersion, result.DatasetVersion)
}

func TestUploadDirectory_ZombieRecovery_DeletesAndReuploads(t *testing.T) {
	dir := makeTempDir(t)
	fullHash, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)

	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return(&models.DatasetVersion{
		ID:   "/datasets/x/versions/zombie",
		Tags: map[string]string{},
	}, nil).Once()
	client.On(
		"DeleteDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return(nil).Once()
	client.On(
		"StartPendingUpload",
		mock.Anything,
		"x",
		mock.Anything,
		"",
	).Return(validPendingUploadResponse(), nil).Once()
	client.On(
		"CreateOrUpdateDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
		mock.MatchedBy(func(dataset *models.DatasetVersion) bool {
			return dataset.Tags["contentHash"] == fullHash
		}),
	).Return(&models.DatasetVersion{ID: "/datasets/x/versions/new"}, nil).Once()
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	res, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.NoError(t, err)

	assert.False(t, res.Skipped)
	assert.False(t, res.Collision)
	assert.Equal(t, "/datasets/x/versions/new", res.DatasetResourceID)
	assert.Equal(t, 1, runner.calls)
}

func TestUploadDirectory_HashCollision_ReturnsCollisionFlag(t *testing.T) {
	dir := makeTempDir(t)

	// Existing version exists with a *different* contentHash → 49-char prefix
	// collided but full hashes differ. Caller should retry with unique naming.
	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return(&models.DatasetVersion{
		ID:   "/datasets/x/versions/other",
		Tags: map[string]string{"contentHash": "different-full-hash-value"},
	}, nil).Once()
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	res, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.NoError(t, err)

	assert.True(t, res.Collision)
	assert.False(t, res.Skipped)
	assert.Equal(t, 0, runner.calls)
}

func TestUploadDirectory_NoExistingVersion_FullUploadWithSentinel(t *testing.T) {
	dir := makeTempDir(t)
	fullHash, err := ComputeDirectoryHash(dir)
	require.NoError(t, err)

	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return((*models.DatasetVersion)(nil), nil).Once()
	client.On(
		"StartPendingUpload",
		mock.Anything,
		"x",
		mock.Anything,
		"",
	).Return(validPendingUploadResponse(), nil).Once()
	client.On(
		"CreateOrUpdateDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
		mock.MatchedBy(func(dataset *models.DatasetVersion) bool {
			return dataset.Tags["contentHash"] == fullHash &&
				dataset.DataType == "uri_folder"
		}),
	).Return(&models.DatasetVersion{ID: "/datasets/x/versions/v1"}, nil).Once()
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	res, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.NoError(t, err)

	assert.Equal(t, "/datasets/x/versions/v1", res.DatasetResourceID)
	assert.False(t, res.Skipped)
	assert.False(t, res.Collision)
	assert.Equal(t, 1, runner.calls)
}

func TestUploadDirectory_MissingSASURI_ReturnsError(t *testing.T) {
	dir := makeTempDir(t)
	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return((*models.DatasetVersion)(nil), nil).Once()
	client.On(
		"StartPendingUpload",
		mock.Anything,
		"x",
		mock.Anything,
		"",
	).Return(&models.PendingUploadResponse{
		BlobReference: &models.BlobReference{
			BlobURI: "https://storage.blob.core.windows.net/c/datasets/x/v1",
		},
	}, nil).Once()
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no SAS URI")
	assert.Equal(t, 0, runner.calls, "azcopy must not be invoked without a SAS URI")
}

func TestUploadDirectory_NilBlobReference_ReturnsError(t *testing.T) {
	dir := makeTempDir(t)
	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return((*models.DatasetVersion)(nil), nil).Once()
	client.On(
		"StartPendingUpload",
		mock.Anything,
		"x",
		mock.Anything,
		"",
	).Return(&models.PendingUploadResponse{BlobReference: nil}, nil).Once()
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no SAS URI")
}

func TestUploadDirectory_AzcopyFailure_PropagatesError(t *testing.T) {
	dir := makeTempDir(t)
	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return((*models.DatasetVersion)(nil), nil).Once()
	client.On(
		"StartPendingUpload",
		mock.Anything,
		"x",
		mock.Anything,
		"",
	).Return(validPendingUploadResponse(), nil).Once()
	runner := &fakeUploadRunner{err: errors.New("azcopy exit status 1")}

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to upload files")
	assert.Contains(t, err.Error(), "azcopy exit status 1")
}

func TestUploadDirectory_GetError_Propagates(t *testing.T) {
	dir := makeTempDir(t)
	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return((*models.DatasetVersion)(nil), errors.New("network down")).Once()
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check existing dataset version")
}

func TestUploadDirectory_ZombieDeleteFails_Propagates(t *testing.T) {
	dir := makeTempDir(t)
	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return(&models.DatasetVersion{
		ID:   "/datasets/x/versions/z",
		Tags: map[string]string{},
	}, nil).Once()
	client.On(
		"DeleteDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return(errors.New("delete denied")).Once()
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(context.Background(), dir, "x", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete zombie")
}

func TestUploadDirectory_MissingLocalPath_ReturnsError(t *testing.T) {
	client := newMockUploadClient(t)
	runner := &fakeUploadRunner{}
	svc := &UploadService{client: client, azcopyRunner: runner}

	_, err := svc.UploadDirectory(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"), "x", "desc")
	require.Error(t, err)
}

func TestUploadDirectoryNoDedup_SkipsLookupAndUploadsWithoutTag(t *testing.T) {
	dir := makeTempDir(t)
	client := newMockUploadClient(t)
	client.On(
		"StartPendingUpload",
		mock.Anything,
		"x",
		"1",
		"",
	).Return(validPendingUploadResponse(), nil).Once()
	client.On(
		"CreateOrUpdateDatasetVersion",
		mock.Anything,
		"x",
		"1",
		mock.MatchedBy(func(dataset *models.DatasetVersion) bool {
			return dataset.Tags == nil
		}),
	).Return(&models.DatasetVersion{ID: "/datasets/x/versions/1"}, nil).Once()
	runner := &fakeUploadRunner{}

	svc := &UploadService{client: client, azcopyRunner: runner}
	res, err := svc.UploadDirectoryNoDedup(context.Background(), dir, "x", "1", "desc")
	require.NoError(t, err)

	assert.Equal(t, "/datasets/x/versions/1", res.DatasetResourceID)
	assert.Equal(t, 1, runner.calls)
}

func TestUploadDirectory_PassesAbsolutePathToAzcopy(t *testing.T) {
	dir := makeTempDir(t)
	client := newMockUploadClient(t)
	client.On(
		"GetDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
	).Return((*models.DatasetVersion)(nil), nil).Once()
	client.On(
		"StartPendingUpload",
		mock.Anything,
		"x",
		mock.Anything,
		"",
	).Return(validPendingUploadResponse(), nil).Once()
	client.On(
		"CreateOrUpdateDatasetVersion",
		mock.Anything,
		"x",
		mock.Anything,
		mock.AnythingOfType("*models.DatasetVersion"),
	).Return(&models.DatasetVersion{ID: "/datasets/x/versions/v1"}, nil).Once()
	runner := &fakeUploadRunner{}

	// Use a relative path to verify the service resolves it before invoking azcopy.
	// Chdir to the temp dir's parent so the relative path is well-defined across
	// drives on Windows (where t.TempDir() may be on a different volume than cwd).
	t.Chdir(filepath.Dir(dir))
	rel := filepath.Base(dir)

	svc := &UploadService{client: client, azcopyRunner: runner}
	_, err := svc.UploadDirectory(t.Context(), rel, "x", "desc")
	require.NoError(t, err)

	assert.True(t, filepath.IsAbs(runner.lastSrc),
		"azcopy must be invoked with an absolute path, got %q", runner.lastSrc)
}

// Compile-time checks that test doubles satisfy the unexported interfaces.
var (
	_ uploadClient = (*trainingmocks.UploadClient)(nil)
	_ uploadRunner = (*fakeUploadRunner)(nil)
)
