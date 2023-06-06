package recording

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/ioutil"
	"net/http"

	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

// Capture collects important details of a request and response into a cassette interaction.
func capture(req *http.Request, resp *http.Response) (*cassette.Interaction, error) {
	interaction := cassette.Interaction{
		Request: cassette.Request{
			Proto:            req.Proto,
			ProtoMajor:       req.ProtoMajor,
			ProtoMinor:       req.ProtoMinor,
			ContentLength:    req.ContentLength,
			TransferEncoding: req.TransferEncoding,
			Trailer:          req.Trailer,
			Host:             req.Host,
			RemoteAddr:       req.RemoteAddr,
			RequestURI:       req.RequestURI,
			Form:             req.Form,
			Headers:          req.Header.Clone(),
			URL:              req.URL.String(),
			Method:           req.Method,
		},
		Response: cassette.Response{
			Proto:            resp.Proto,
			ProtoMajor:       resp.ProtoMajor,
			ProtoMinor:       resp.ProtoMinor,
			TransferEncoding: resp.TransferEncoding,
			Trailer:          resp.Trailer,
			Uncompressed:     true,
			Headers:          resp.Header.Clone(),
			Status:           resp.Status,
			Code:             resp.StatusCode,
		},
	}

	interaction.Request.Headers.Set("Authorization", "Sanitized")
	interaction.Response.Headers.Set("Authorization", "Sanitized")

	// Read bytes and reset the body
	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body = ioutil.NopCloser(bytes.NewBuffer(content))

	// Read the potentially g-zipped content
	contentCopy := make([]byte, len(content))
	copy(contentCopy, content)
	var contentReader io.Reader = bytes.NewReader(contentCopy)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		contentReader, err = gzip.NewReader(contentReader)
		if err != nil {
			return nil, err
		}

		interaction.Response.Headers.Del("Content-Encoding")
	}
	body, err := ioutil.ReadAll(contentReader)
	if err != nil {
		return nil, err
	}

	interaction.Response.Body = string(body)
	interaction.Response.ContentLength = int64(len(body))

	return &interaction, nil
}
