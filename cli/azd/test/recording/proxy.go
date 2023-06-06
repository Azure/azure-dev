package recording

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/exp/slog"
)

// Like http.Handler, except with a io.Writer instead of a http.ResponseWriter.
type httpHandler interface {
	ServeConn(w io.Writer, req *http.Request)
}

// connectProxy is a proxy that supports for both HTTPS connect and direct HTTP methods.
type connectProxy struct {
	TLS *tls.Config

	Log *slog.Logger

	HttpHandler httpHandler
}

func (p *connectProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p.Log.Debug("connectProxy: %s %s", req.Method, req.URL)

	conn, err := p.hijack(w)
	if err != nil {
		panic(err)
	}

	// Handle HTTPS CONNECT
	if req.Method == http.MethodConnect {
		p.Log.Debug("CONNECT requested to %v (from %v)", req.Host, req.RemoteAddr)
		p.connectThenServe(conn, req)
	} else { // Handle direct HTTP
		p.HttpHandler.ServeConn(conn, req)
	}
}

// "hijack" client connection to get a TCP (or TLS) socket we can read
// and write arbitrary data to/from.
func (p *connectProxy) hijack(w http.ResponseWriter) (net.Conn, error) {
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

func (p *connectProxy) connectThenServe(clientConn net.Conn, connectReq *http.Request) {
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
		log.Fatal(err)
	}
	return u
}
