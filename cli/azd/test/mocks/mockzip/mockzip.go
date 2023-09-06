package mockzip

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
)

type File struct {
	// Name of the path in the archive. Forward slash should be the path separator.
	Name string

	// Content of the file.
	Content string
}

func CreateTar(files []File) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for _, file := range files {
		hdr := &tar.Header{
			Name: file.Name,
			Mode: 0644,
			Size: int64(len(file.Content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(file.Content)); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return &buf, nil
}

func CompressGzip(content *bytes.Buffer) (*bytes.Buffer, error) {
	var compressedBuffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBuffer)
	defer gzipWriter.Close()

	_, err := io.Copy(gzipWriter, content)
	if err != nil {
		return nil, err
	}

	return &compressedBuffer, nil
}

func GzippedTar(files []File) (*bytes.Buffer, error) {
	bytes, err := CreateTar(files)
	if err != nil {
		return nil, err
	}

	return CompressGzip(bytes)
}

func Zip(files []File) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, file := range files {
		f, err := zw.Create(file.Name)
		if err != nil {
			return nil, err
		}
		_, err = f.Write([]byte(file.Content))
		if err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}

	return &buf, nil
}
