// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

const taskUploadURLPath = environmentCollectionPath + "/tasks/upload-url"
const taskFinalizePath = environmentCollectionPath + "/tasks/finalize"

// harborTaskBundle references a task-directory tarball in the project's RLE storage.
type harborTaskBundle struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

// harborTask is one Harbor task published with an environment version.
type harborTask struct {
	Name         string           `json:"name"`
	AcrImagePath string           `json:"acrImagePath"`
	Bundle       harborTaskBundle `json:"bundle"`
}

// harborTaskStatus is a published task with its own disk-image conversion status.
type harborTaskStatus struct {
	Name                      string `json:"name"`
	AcrImagePath              string `json:"acrImagePath,omitempty"`
	DiskImageConversionStatus string `json:"diskImageConversionStatus,omitempty"`
	DiskImageConversionError  string `json:"diskImageConversionError,omitempty"`
}

type pagedHarborTaskResponse struct {
	Data                  []harborTaskStatus `json:"data"`
	NextContinuationToken string             `json:"nextContinuationToken,omitempty"`
}

type harborUploadURLResponse struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	UploadURL string `json:"uploadUrl,omitempty"`
	// StagingPath names the blob the upload URL writes; finalize publishes it.
	StagingPath string `json:"stagingPath,omitempty"`
}

// harborBundleHash is the content hash that names a bundle in RLE storage.
func harborBundleHash(bundle []byte) string {
	sum := sha256.Sum256(bundle)
	return hex.EncodeToString(sum[:])
}

// uploadHarborBundle stores the bundle in project storage unless it is already there, and returns
// its reference. RLE only issues a single-blob write URL; the bytes go straight to storage.
func (c *rleClient) uploadHarborBundle(ctx context.Context, bundle []byte) (harborTaskBundle, error) {
	hash := harborBundleHash(bundle)
	var target harborUploadURLResponse
	if err := c.do(ctx, http.MethodPost, taskUploadURLPath,
		map[string]string{"sha256": hash}, &target); err != nil {
		return harborTaskBundle{}, err
	}
	ref := harborTaskBundle{Path: target.Path, Sha256: hash}
	if target.Exists {
		return ref, nil
	}
	if target.UploadURL == "" {
		return ref, fmt.Errorf("RLE returned no upload URL for the Harbor bundle")
	}
	uploadURL, err := url.Parse(target.UploadURL)
	if err != nil || uploadURL.Scheme != "https" || uploadURL.Host == "" || uploadURL.User != nil {
		return ref, errors.New("RLE returned an invalid Harbor bundle upload URL; expected HTTPS without user credentials")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL.String(), bytes.NewReader(bundle))
	if err != nil {
		return ref, errors.New("could not create Harbor bundle upload request")
	}
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("x-ms-version", "2021-08-06")
	req.Header.Set("Content-Type", "application/gzip")
	uploadClient := *c.httpClient
	uploadClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := uploadClient.Do(req)
	if err != nil {
		// url.Error includes the signed URL; retain only its underlying transport failure.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return ref, fmt.Errorf("upload Harbor bundle: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ref, fmt.Errorf("upload Harbor bundle: storage returned status %d", resp.StatusCode)
	}
	// RLE verifies the staged bytes against the hash before publishing them at the content-addressed path.
	if err := c.do(ctx, http.MethodPost, taskFinalizePath,
		map[string]string{"sha256": hash, "stagingPath": target.StagingPath}, nil); err != nil {
		return ref, err
	}
	return ref, nil
}
