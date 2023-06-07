package recording

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sethvargo/go-retry"
	"golang.org/x/exp/slog"
	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

// recorderProxy is a server that records and plays back interactions. The mode is determined by the value of Playback.
//
// In record mode, recorderProxy responds to requests by proxy-ing to the upstream server
// while recording the interactions.
//
// In playback mode, recorderProxy replays interactions from previously stored interactions.
type recorderProxy struct {
	Log *slog.Logger

	// Panic specifies the function to call when the server panics.
	// If nil, `panic` is used.
	Panic func(msg string)

	// If true, playing back from recording.
	// Otherwise, recording.
	Playback bool

	// Cst contains the cassette to save interactions to, or to playback interactions from saved recording.
	Cst *cassette.Cassette
}

func (p *recorderProxy) ServeConn(conn io.Writer, req *http.Request) {
	p.Log.Debug("recorderProxy: incoming request", "url", req.URL)

	if p.Playback {
		interact, err := p.Cst.GetInteraction(req)
		if err != nil {
			p.panic("recorderProxy: %v", err)
		}

		resp, err := interact.GetHTTPResponse()
		if err != nil {
			p.panic("recorderProxy: %v", err)
		}

		err = resp.Write(conn)
		if err != nil {
			p.panic(err.Error())
		}
	} else {
		var resp *http.Response
		var err error

		err = retry.Do(
			context.Background(),
			retry.WithMaxRetries(3, retry.NewConstant(100*time.Millisecond)),
			func(_ context.Context) error {
				resp, err = http.DefaultClient.Do(req)
				return retry.RetryableError(err)
			})
		if err != nil {
			p.panic("recorderProxy: error sending request to target: %v", err)
		}
		// Always use chunked encoding for sending the response back.
		resp.TransferEncoding = []string{"chunked"}
		interaction, err := capture(req, resp)
		if err != nil {
			p.panic(fmt.Sprintf("recorderProxy: error capturing interaction: %v", err))
		}

		p.Cst.AddInteraction(interaction)

		p.Log.Debug("recorderProxy: outgoing response", "url", req.URL, "status", resp.Status)
		// Send the target server's response back to the client.
		if err := resp.Write(conn); err != nil {
			p.panic("recorderProxy: error writing response: %v", err)
		}
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
	p.Log.Debug("connectHandler:", "method", req.Method, "url", req.URL)

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
		if err == io.EOF {
			break
		} else if err != nil {
			panic(err)
		}

		changeRequestToTarget(req, connectReq.Host)
		p.HttpHandler.ServeConn(tlsConn, req)
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
