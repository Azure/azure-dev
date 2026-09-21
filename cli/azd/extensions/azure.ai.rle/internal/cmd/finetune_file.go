// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

const finetuneFilesPath = "/openai/v1/files"

type finetuneFileResource struct {
	Id string `json:"id"`
}

func (c *finetuneClient) uploadFile(ctx context.Context, filePath string) (*finetuneFileResource, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open local file: %w", err)
	}

	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect local file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("local file must be a regular file")
	}

	bodyReader, bodyWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(bodyWriter)
	writeResult := make(chan error, 1)
	go func() {
		defer file.Close()

		if err := writeFinetuneFileUpload(multipartWriter, file, filepath.Base(filePath)); err != nil {
			_ = bodyWriter.CloseWithError(err)
			writeResult <- err
			return
		}
		writeResult <- bodyWriter.Close()
	}()

	var result finetuneFileResource
	requestErr := c.doWithReader(
		ctx,
		http.MethodPost,
		finetuneFilesPath,
		nil,
		bodyReader,
		multipartWriter.FormDataContentType(),
		&result,
	)
	if requestErr != nil {
		_ = bodyReader.CloseWithError(requestErr)
	} else {
		_ = bodyReader.Close()
	}
	writeErr := <-writeResult
	if requestErr != nil {
		return nil, fmt.Errorf("upload fine-tuning file: %w", requestErr)
	}
	if writeErr != nil {
		return nil, fmt.Errorf("write fine-tuning file upload: %w", writeErr)
	}
	return &result, nil
}

func writeFinetuneFileUpload(writer *multipart.Writer, file *os.File, fileName string) error {
	if err := writer.WriteField("purpose", "fine-tune"); err != nil {
		return fmt.Errorf("write purpose field: %w", err)
	}

	filePart, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return fmt.Errorf("create file form part: %w", err)
	}
	if _, err := io.Copy(filePart, file); err != nil {
		return fmt.Errorf("write file content: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart form: %w", err)
	}
	return nil
}
