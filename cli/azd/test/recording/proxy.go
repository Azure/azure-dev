package recording

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

type logWriter struct {
	t  *testing.T
	sb strings.Builder
}

func (l *logWriter) Write(bytes []byte) (n int, err error) {
	for i, b := range bytes {
		err = l.sb.WriteByte(b)
		if err != nil {
			return i, err
		}

		if b == '\n' {
			l.t.Logf(l.sb.String())
			l.sb.Reset()
		}
	}
	return len(bytes), nil
}

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
			Headers:          req.Header,
			URL:              req.URL.String(),
			Method:           req.Method,
		},
		Response: cassette.Response{
			Proto:            resp.Proto,
			ProtoMajor:       resp.ProtoMajor,
			ProtoMinor:       resp.ProtoMinor,
			TransferEncoding: resp.TransferEncoding,
			Trailer:          resp.Trailer,
			//ContentLength:    resp.ContentLength,
			Uncompressed: true,
			Headers:      resp.Header.Clone(),
			Status:       resp.Status,
			Code:         resp.StatusCode,
		},
	}

	// Read bytes and reset the body
	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body = ioutil.NopCloser(bytes.NewBuffer(content))

	// Read the potentially g-zipped content
	cloned := bytes.Clone(content)
	var contentReader io.Reader = bytes.NewReader(cloned)
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

type RecordMode int64

const (
	Unknown RecordMode = iota
	Playback
	Record
	Live
)

type recordOption struct {
	mode RecordMode
}

type Options interface {
	Apply(r *recordOption)
}

func WithRecordMode(mode RecordMode) Options {
	return modeOption{mode: mode}
}

type modeOption struct {
	mode RecordMode
}

func (m modeOption) Apply(r *recordOption) {
	r.mode = m.mode
}

func Start(t *testing.T, cli *azdcli.CLI, opts ...Options) {
	opt := recordOption{}
	for _, o := range opts {
		o.Apply(&opt)
	}

	if opt.mode == Unknown {
		_, err := cassette.Load(t.Name())
		if errors.Is(err, os.ErrNotExist) {
			if os.Getenv("CI") != "" {
				t.Fatalf(
					"no recording available for %s. record this locally before re-running the pipeline",
					t.Name())
			}

			t.Logf("playback not available for %s. recording locally", t.Name())
			opt.mode = Record
		} else if err != nil {
			t.Fatalf("failed to load cassette: %v", err)
		} else {
			opt.mode = Playback
		}
	}

	writer := &logWriter{t: t}

	switch opt.mode {
	case Live:
		return
	case Playback:
		cst, err := cassette.Load(t.Name())
		if err != nil {
			t.Fatalf("failed to load cassette: %v", err)
		}
		playback := &playbackServer{
			cst:      cst,
			ErrorLog: log.New(writer, "playback: ", log.Lshortfile)}
		server := httptest.NewTLSServer(playback)
		playback.Config = server.TLS
		cli.Env = append(cli.Env, "HTTP_PROXY="+server.URL, "HTTPS_PROXY="+server.URL)
		t.Logf("playbackServer started at %s", server.URL)
		t.Cleanup(server.Close)
	case Record:
		proxy := &recordingProxy{
			ErrorLog: log.New(writer, "proxy: ", log.Lshortfile),
		}

		cst := cassette.New(t.Name())
		proxy.ModifyResponse = func(req *http.Request, resp *http.Response) error {
			interaction, err := capture(req, resp)
			if err != nil {
				return err
			}

			cst.AddInteraction(interaction)
			return nil
		}

		server := httptest.NewTLSServer(proxy)
		proxy.Config = server.TLS
		t.Logf("recordingProxy started at %s", server.URL)
		cli.Env = append(cli.Env, "HTTP_PROXY="+server.URL, "HTTPS_PROXY="+server.URL)
		t.Cleanup(func() {
			server.Close()
			if !t.Failed() {
				if err := cst.Save(); err != nil {
					t.Errorf("failed to save recording: %v", err)
				}
			}
		})
	}
}

type playbackServer struct {
	cst    *cassette.Cassette
	Config *tls.Config

	ErrorLog *log.Logger
}

func (p *playbackServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p.ErrorLog.Printf("playback: %s %s", req.Method, req.URL)
	if req.Method == http.MethodConnect {
		p.proxyConnect(w, req)
	} else {
		http.Error(w, "this proxy only supports CONNECT", http.StatusMethodNotAllowed)
	}
}

func (p *playbackServer) proxyConnect(w http.ResponseWriter, req *http.Request) {
	// "hijack" client connection to get a TCP (or TLS) socket we can read
	// and write arbitrary data to/from.
	hj, ok := w.(http.Hijacker)
	if !ok {
		p.ErrorLog.Fatal("http server doesn't support hijacking connection")
	}

	clientConn, _, err := hj.Hijack()
	if err != nil {
		log.Fatal("http hijacking failed")
	}

	// Send an HTTP OK response back to the client; this initiates the CONNECT
	// tunnel. From this point on the client will assume it's connected directly
	// to the target.
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n")); err != nil {
		log.Fatal("error writing status to client:", err)
	}

	tlsConn := tls.Server(clientConn, p.Config)
	defer tlsConn.Close()

	// Create a buffered reader for the client connection so that we can use for http.
	connReader := bufio.NewReader(tlsConn)

	// Run the proxy in a loop until the client closes the connection.
	for {
		// Read an HTTP request from the client; the request is sent over TLS that
		// connReader is configured to serve. The read will run a TLS handshake in
		// the first invocation (we could also call tlsConn.Handshake explicitly
		// before the loop, but this isn't necessary).
		// Note that while the client believes it's talking across an encrypted
		// channel with the target, the proxy gets these requests in "plain text"
		// because of the MITM setup.
		r, err := http.ReadRequest(connReader)
		if err == io.EOF {
			break
		} else if err != nil {
			log.Fatal(err)
		}

		changeRequestToTarget(r, req.Host)
		interact, err := p.cst.GetInteraction(r)
		if err != nil {
			resp := &http.Response{}
			resp.StatusCode = http.StatusBadRequest
			p.ErrorLog.Printf("playbackServer : %v", err)
			if err := resp.Write(tlsConn); err != nil {
				log.Println("error writing response back:", err)
			}
			return
		}

		resp, err := interact.GetHTTPResponse()
		if err != nil {
			resp := &http.Response{}
			resp.StatusCode = http.StatusBadRequest
			p.ErrorLog.Printf("playbackServer : %v", err)
			if err := resp.Write(tlsConn); err != nil {
				log.Println("error writing response back:", err)
			}
			return
		}

		// Send the target server's response back to the client.
		if err := resp.Write(tlsConn); err != nil {
			log.Println("error writing response back:", err)
		}
	}
}

type proxiedHandler interface {
	http.Handler
	Serve(req *http.Request) (*http.Response, error)
}

// connectProxy is a server that responds to incoming CONNECT requests,
// creating a TLS connection back to the client.
type connectProxy struct {
	handler proxiedHandler

	TLS *tls.Config

	log *log.Logger
}

func (c *connectProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodConnect {
		c.proxyConnect(w, req)
	} else {
		c.handler.ServeHTTP(w, req)
	}
}

func (c *connectProxy) proxyConnect(w http.ResponseWriter, req *http.Request) {
	// "hijack" client connection to get a TCP (or TLS) connection we can read
	// and write arbitrary data to/from.
	hj, ok := w.(http.Hijacker)
	if !ok {
		c.log.Panicf("http server doesn't support hijacking connection")
	}

	clientConn, _, err := hj.Hijack()
	if err != nil {
		c.log.Panicf("http hijacking failed")
	}

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n")); err != nil {
		c.log.Panicf(fmt.Sprintf("error writing status to client: %v", err))
	}

	// Create a TLS connection to the client.
	// Calling tlsConn.Handshake is optional. It will automatically happen as part of the first request.
	tlsConn := tls.Server(clientConn, c.TLS)
	defer tlsConn.Close()

	// Buffered reader for http
	connReader := bufio.NewReader(tlsConn)

	// Run the proxy in a loop until the client closes the connection.
	for {
		// Read an HTTP request from the client; the request is sent over TLS that
		// connReader is configured to serve.
		r, err := http.ReadRequest(connReader)
		if err == io.EOF {
			break
		} else if err != nil {
			c.log.Fatal(err)
		}

		resp, err := c.handler.Serve(r)
		if err != nil {

		}

		// Send the target server's response back to the client.
		if err := resp.Write(tlsConn); err != nil {
			log.Println("error writing response back:", err)
		}
	}
}

// recordingProxy is a server that responds to incoming requests by either
// proxy-ing from the upstream server or responding from playback.
//
// The mode is determined by the presence of the x-recording-mode header.
type recordingProxy struct {
	anyFailed bool

	Config *tls.Config

	// ErrorLog specifies an optional logger for errors accepting
	// connections, unexpected behavior from handlers, and
	// underlying FileSystem errors.
	ErrorLog *log.Logger

	// ModifyResponse is a function which is called when the backend returns a response at all,
	// with any HTTP status code.
	//
	// req is the unmodified received request, and resp is the backend response.
	ModifyResponse func(req *http.Request, resp *http.Response) error
}

func (p *recordingProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p.ErrorLog.Printf("recordingProxy: %s %s", req.Method, req.URL)

	if req.Method == http.MethodConnect {
		p.proxyConnect(w, req)
	} else {
		http.Error(w, "this proxy only supports CONNECT", http.StatusMethodNotAllowed)
	}
}

func (p *recordingProxy) proxyConnect(w http.ResponseWriter, req *http.Request) {
	p.ErrorLog.Printf("CONNECT requested to %v (from %v)", req.Host, req.RemoteAddr)

	// "hijack" client connection to get a TCP (or TLS) socket we can read
	// and write arbitrary data to/from.
	hj, ok := w.(http.Hijacker)
	if !ok {
		p.ErrorLog.Fatal("http server doesn't support hijacking connection")
	}

	clientConn, _, err := hj.Hijack()
	if err != nil {
		log.Fatal("http hijacking failed")
	}

	// Send an HTTP OK response back to the client; this initiates the CONNECT
	// tunnel. From this point on the client will assume it's connected directly
	// to the target.
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n")); err != nil {
		log.Fatal("error writing status to client:", err)
	}

	// Calling tlsConn.Handshake is optional. It will automatically happen as part of the first request.
	tlsConn := tls.Server(clientConn, p.Config)
	defer tlsConn.Close()

	// Create a buffered reader for the client connection so that we can use for http.
	connReader := bufio.NewReader(tlsConn)

	// Run the proxy in a loop until the client closes the connection.
	for {
		// Read an HTTP request from the client; the request is sent over TLS that
		// connReader is configured to serve.
		r, err := http.ReadRequest(connReader)
		if err == io.EOF {
			break
		} else if err != nil {
			log.Fatal(err)
		}

		// We can dump the request; log it, modify it...
		if b, err := httputil.DumpRequest(r, false); err == nil {
			log.Printf("incoming request:\n%s\n", string(b))
		}

		// Take the original request and changes its destination to be forwarded
		// to the target server.
		changeRequestToTarget(r, req.Host)

		// Send the request to the target server and log the response.
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			log.Fatal("error sending request to target:", err)
		}
		if b, err := httputil.DumpResponse(resp, false); err == nil {
			log.Printf("target response:\n%s\n", string(b))
		}
		p.ModifyResponse(r, resp)

		// Send the target server's response back to the client.
		if err := resp.Write(tlsConn); err != nil {
			log.Println("error writing response back:", err)
		}
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
		log.Fatal(err)
	}
	return u
}

func (p *recordingProxy) RespondErr(w http.ResponseWriter, req *http.Request, err error) {
	w.WriteHeader(http.StatusBadGateway)
	w.Write([]byte(fmt.Sprintf("recordingProxy error: %v", err)))

	p.ErrorLog.Printf("recordingProxy error: %v", err)
	p.anyFailed = true
}
