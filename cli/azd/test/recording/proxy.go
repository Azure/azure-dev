// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package recording

import (
	"bufio"
	"compress/gzip"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/dnaeon/go-vcr.v3/recorder"
)

// gzipReader wraps a response body so it can lazily
// call gzip.NewReader on the first call to Read.
// Inspired by http2gzipReader in http/h2_bundle.go
type http2gzipReader struct {
	body io.ReadCloser // underlying Response.Body
	zr   *gzip.Reader  // lazily-initialized gzip reader
	zerr error         // sticky error
}

func (gz *http2gzipReader) Read(p []byte) (n int, err error) {
	if gz.zerr != nil {
		return 0, gz.zerr
	}
	if gz.zr == nil {
		gz.zr, err = gzip.NewReader(gz.body)
		if err != nil {
			gz.zerr = err
			return 0, err
		}
	}
	return gz.zr.Read(p)
}

func (gz *http2gzipReader) Close() error {
	if err := gz.body.Close(); err != nil {
		return err
	}
	gz.zerr = fs.ErrClosed
	return nil
}

// A HTTP round tripper that automatically decompresses gzip content.
type gzip2HttpRoundTripper struct {
	transport http.RoundTripper
}

func (u *gzip2HttpRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := u.transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if resp.Header.Get("Content-Encoding") == "gzip" {
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1
		resp.Body = &http2gzipReader{body: resp.Body}
		resp.Uncompressed = true
	}

	return resp, nil
}

// recorderProxy is a server that communicates using HTTP over an underlying connection using [go-vcr/recorder]
// to record or playback interactions.
type recorderProxy struct {
	Log *slog.Logger

	// Panic specifies the function to call when the server panics.
	// If nil, `panic` is used.
	Panic func(msg string)

	// The recorder that will be used to record or replay interactions.
	Recorder *recorder.Recorder
}

func (p *recorderProxy) ServeConn(conn io.Writer, req *http.Request) {
	p.Log.Debug("recorderProxy: incoming request", "url", req.URL.String())

	resp, err := p.Recorder.RoundTrip(req)
	if err != nil {
		p.panic(fmt.Sprintf("%s %s: %s", req.Method, req.URL.String(), err.Error()))
	}

	if err != nil {
		// report the error back to the client
		resp = &http.Response{}
		resp.StatusCode = http.StatusBadRequest
		resp.Header.Add("x-ms-error-code", err.Error())
		resp.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":{"code":"%s"}}`, err.Error())))
	}

	// Always use chunked encoding for transferring the response back, which handles large response bodies.
	resp.TransferEncoding = []string{"chunked"}
	err = resp.Write(conn)
	if err != nil {
		p.panic(err.Error())
	}
}

// panic calls the user-defined Panic function if set, otherwise the default panic function.
func (p *recorderProxy) panic(msg string, args ...interface{}) {
	if p.Panic != nil {
		p.Panic(fmt.Sprintf(msg, args...))
	} else {
		panic(fmt.Sprintf(msg, args...))
	}
}

// Like http.Handler, except writing to an underlying network connection (io.Writer),
// instead of a http.ResponseWriter.
type httpHandler interface {
	ServeConn(w io.Writer, req *http.Request)
}

// connectHandler is a http.Handler server implementation that provides support for negotiating
// HTTPS connect and direct HTTP methods proxy protocols which is relayed to an underlying HttpHandler.
//
// HttpHandler can be set to an implementation that handles the HTTP requests, with the CONNECT handshake abstracted away.
type connectHandler struct {
	// TLS configuration that will be used for the TLS CONNECT tunnel.
	TLS *tls.Config

	// Log is the logger that will be used to log messages.
	Log *slog.Logger

	// The HTTP handler that will be used to handle the HTTP requests.
	HttpHandler httpHandler
}

func (p *connectHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p.Log.Debug("connectHandler: incoming", "method", req.Method, "url", req.URL.String())

	conn, err := p.hijack(w)
	if err != nil {
		panic(err)
	}

	// Handle HTTPS CONNECT
	if req.Method == http.MethodConnect {
		p.Log.Debug("connectHandler: CONNECT requested to ", "host", req.Host, "remoteAddr", req.RemoteAddr)
		p.connectThenServe(conn, req)
	} else { // Handle direct HTTP
		p.HttpHandler.ServeConn(conn, req)
	}
}

// hijack "hijacks" the client connection to get a TCP (or TLS) socket we can read
// and write arbitrary data to/from.
func (p *connectHandler) hijack(w http.ResponseWriter) (net.Conn, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("http server doesn't support hijacking connection")
	}

	clientConn, _, err := hj.Hijack()
	if err != nil {
		return nil, fmt.Errorf("http hijacking failed: %w", err)
	}

	return clientConn, nil
}

func (p *connectHandler) connectThenServe(clientConn net.Conn, connectReq *http.Request) {
	// Send an HTTP OK response back to the client; this initiates the CONNECT
	// tunnel. From this point on the client will assume it's connected directly
	// to the target.
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n")); err != nil {
		panic(fmt.Sprintf("error writing status to client: %v", err))
	}

	// Calling tlsConn.Handshake is optional. It will automatically happen as part of the first request.
	tlsConn := tls.Server(clientConn, p.TLS)
	defer tlsConn.Close()

	// Create a buffered reader for the client connection so that we can use for http.
	connReader := bufio.NewReader(tlsConn)

	// Run the proxy in a loop until the client closes the connection.
	for {
		// Read an HTTP request from the client; the request is sent over TLS that
		// connReader is configured to serve.
		req, err := http.ReadRequest(connReader)
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			// Terminate the connection if we fail to read the request.
			p.Log.Error("connectHandler failed to read HTTP request", "error", err.Error())
			return
		}

		changeRequestToTarget(req, connectReq.Host)
		p.HttpHandler.ServeConn(tlsConn, req)

		// Always close the request body
		_ = req.Body.Close()
	}
}

// changeRequestToTarget modifies req to be re-routed to the given target;
// the target should be taken from the Host of the original tunnel (CONNECT)
// request.
func changeRequestToTarget(req *http.Request, targetHost string) {
	targetUrl := addrToUrl(targetHost)
	targetUrl.Path = req.URL.Path
	targetUrl.RawQuery = req.URL.RawQuery
	req.URL = targetUrl
	// Make sure this is unset for sending the request through a client
	req.RequestURI = ""
}

func addrToUrl(addr string) *url.URL {
	if !strings.HasPrefix(addr, "https") {
		addr = "https://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		panic(err)
	}

	return u
}
