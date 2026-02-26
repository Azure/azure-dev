// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// PendingUploadResponse is the API response from startPendingUpload.
type PendingUploadResponse struct {
	BlobReferenceForConsumption  *BlobReferenceForConsumption `json:"blobReferenceForConsumption"`
	ImageReferenceForConsumption interface{}                  `json:"imageReferenceForConsumption"`
	TemporaryDataReferenceID     string                       `json:"temporaryDataReferenceId"`
	TemporaryDataReferenceType   *string                      `json:"temporaryDataReferenceType"`
}

// BlobReferenceForConsumption contains the blob storage details for upload.
type BlobReferenceForConsumption struct {
	BlobURI             string         `json:"blobUri"`
	StorageAccountArmID string         `json:"storageAccountArmId"`
	Credential          BlobCredential `json:"credential"`
	IsSingleFile        bool           `json:"isSingleFile"`
	BlobManifestDigest  *string        `json:"blobManifestDigest"`
	ConnectionName      *string        `json:"connectionName"`
}

// BlobCredential contains the SAS credential for blob upload.
type BlobCredential struct {
	CredentialType string  `json:"credentialType"`
	SasURI         string  `json:"sasUri"`
	WasbsURI       *string `json:"wasbsUri"`
}
