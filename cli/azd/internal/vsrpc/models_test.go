// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
)

func TestProgressMessage_WithMessage(t *testing.T) {
	before := time.Now().Add(-time.Second)
	original := ProgressMessage{
		Message:            "original",
		Severity:           Warning,
		Kind:               Important,
		Code:               "E001",
		AdditionalInfoLink: "https://example.com",
	}

	updated := original.WithMessage("updated text")

	// Message and Time should change
	assert.Equal(t, "updated text", updated.Message)
	assert.True(
		t, updated.Time.After(before),
		"Time should be set to now",
	)

	// Other fields should be preserved
	assert.Equal(t, Warning, updated.Severity)
	assert.Equal(t, Important, updated.Kind)
	assert.Equal(t, "E001", updated.Code)
	assert.Equal(
		t, "https://example.com", updated.AdditionalInfoLink,
	)

	// Original should be unchanged (value receiver)
	assert.Equal(t, "original", original.Message)
}

func TestNewInfoProgressMessage(t *testing.T) {
	before := time.Now()
	msg := newInfoProgressMessage("hello info")
	after := time.Now()

	assert.Equal(t, "hello info", msg.Message)
	assert.Equal(t, Info, msg.Severity)
	assert.Equal(t, Logging, msg.Kind)
	assert.True(
		t,
		!msg.Time.Before(before) && !msg.Time.After(after),
		"Time should be approximately now",
	)
	assert.Empty(t, msg.Code)
	assert.Empty(t, msg.AdditionalInfoLink)
}

func TestNewImportantProgressMessage(t *testing.T) {
	before := time.Now()
	msg := newImportantProgressMessage("hello important")
	after := time.Now()

	assert.Equal(t, "hello important", msg.Message)
	assert.Equal(t, Info, msg.Severity)
	assert.Equal(t, Important, msg.Kind)
	assert.True(
		t,
		!msg.Time.Before(before) && !msg.Time.After(after),
		"Time should be approximately now",
	)
}

func TestMessageSeverity_Values(t *testing.T) {
	assert.Equal(t, MessageSeverity(0), Info)
	assert.Equal(t, MessageSeverity(1), Warning)
	assert.Equal(t, MessageSeverity(2), Error)
}

func TestMessageKind_Values(t *testing.T) {
	assert.Equal(t, MessageKind(0), Logging)
	assert.Equal(t, MessageKind(1), Important)
}

func TestDeleteMode_BitFlags(t *testing.T) {
	// Verify they are distinct bit flags (use EqualValues
	// since iota constants are untyped int, DeleteMode is uint32)
	assert.EqualValues(t, 1, DeleteModeLocal)
	assert.EqualValues(t, 2, DeleteModeAzureResources)

	// Verify they can be combined
	combined := DeleteModeLocal | DeleteModeAzureResources
	assert.True(t, combined&DeleteModeLocal != 0)
	assert.True(t, combined&DeleteModeAzureResources != 0)

	// Verify single flags don't overlap
	assert.EqualValues(
		t, 0, DeleteModeLocal&DeleteModeAzureResources,
	)
}

func TestEnvironment_JSONRoundTrip(t *testing.T) {
	endpoint := "https://api.example.com"
	resourceId := "/subscriptions/sub-id/rg/rg-name"

	env := Environment{
		Name:      "dev",
		IsCurrent: true,
		Properties: map[string]string{
			"Subscription": "sub-123",
			"Location":     "eastus",
		},
		Services: []*Service{
			{
				Name:       "web",
				IsExternal: false,
				Path:       "./src/web",
				Endpoint:   &endpoint,
				ResourceId: &resourceId,
			},
		},
		Values: map[string]string{
			"AZURE_LOCATION": "eastus",
		},
		LastDeployment: &DeploymentResult{
			Success:      true,
			Time:         time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
			Message:      "Deployed successfully",
			DeploymentId: "deploy-abc",
		},
		Resources: []*Resource{
			{
				Name: "rg-dev",
				Type: "Microsoft.Resources/resourceGroups",
				Id:   "/subscriptions/sub-123/resourceGroups/rg-dev",
			},
		},
	}

	data, err := json.Marshal(env)
	require.NoError(t, err)

	var decoded Environment
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, env.Name, decoded.Name)
	assert.Equal(t, env.IsCurrent, decoded.IsCurrent)
	assert.Equal(t, env.Properties, decoded.Properties)
	require.Len(t, decoded.Services, 1)
	assert.Equal(t, "web", decoded.Services[0].Name)
	assert.Equal(t, env.Values, decoded.Values)
	require.NotNil(t, decoded.LastDeployment)
	assert.Equal(
		t, env.LastDeployment.DeploymentId,
		decoded.LastDeployment.DeploymentId,
	)
	require.Len(t, decoded.Resources, 1)
	assert.Equal(t, "rg-dev", decoded.Resources[0].Name)
}

func TestEnvironment_OmitsNilLastDeployment(t *testing.T) {
	env := Environment{
		Name:           "prod",
		LastDeployment: nil,
	}

	data, err := json.Marshal(env)
	require.NoError(t, err)

	var raw map[string]any
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)

	_, has := raw["LastDeployment"]
	assert.False(t, has, "nil LastDeployment should be omitted")
}

func TestService_OmitsNilOptionalFields(t *testing.T) {
	svc := Service{
		Name:       "api",
		IsExternal: true,
		Path:       "./src/api",
		Endpoint:   nil,
		ResourceId: nil,
	}

	data, err := json.Marshal(svc)
	require.NoError(t, err)

	var raw map[string]any
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)

	_, hasEndpoint := raw["Endpoint"]
	_, hasResourceId := raw["ResourceId"]
	assert.False(t, hasEndpoint, "nil Endpoint should be omitted")
	assert.False(
		t, hasResourceId, "nil ResourceId should be omitted",
	)
}

func TestInitializeServerOptions_JSON(t *testing.T) {
	tests := []struct {
		name string
		opts InitializeServerOptions
	}{
		{
			name: "all nil",
			opts: InitializeServerOptions{},
		},
		{
			name: "all set",
			opts: InitializeServerOptions{
				AuthenticationEndpoint:    new("https://auth.local"),
				AuthenticationKey:         new("secret-key"),
				AuthenticationCertificate: new("base64cert=="),
			},
		},
		{
			name: "partial",
			opts: InitializeServerOptions{
				AuthenticationEndpoint: new("https://auth.local"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.opts)
			require.NoError(t, err)

			var decoded InitializeServerOptions
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.opts, decoded)
		})
	}
}

func TestRequestContext_Fields(t *testing.T) {
	rc := RequestContext{
		Session:         Session{Id: "sess-123"},
		HostProjectPath: "/home/user/project",
	}

	assert.Equal(t, "sess-123", rc.Session.Id)
	assert.Equal(t, "/home/user/project", rc.HostProjectPath)
}

func TestEnvironmentInfo_Fields(t *testing.T) {
	info := EnvironmentInfo{
		Name:       "staging",
		IsCurrent:  true,
		DotEnvPath: "/home/user/.env",
	}

	assert.Equal(t, "staging", info.Name)
	assert.True(t, info.IsCurrent)
	assert.Equal(t, "/home/user/.env", info.DotEnvPath)
}

func TestAspireHost_Fields(t *testing.T) {
	host := AspireHost{
		Name: "my-aspire-host",
		Path: "/path/to/apphost.csproj",
		Services: []*Service{
			{Name: "api", Path: "./api"},
			{Name: "web", Path: "./web"},
		},
	}

	assert.Equal(t, "my-aspire-host", host.Name)
	assert.Len(t, host.Services, 2)
}

func TestInitializeAsync_RootPath_NotExists(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	svc := newServerService(s)
	_, err := svc.InitializeAsync(t.Context(), filepath.Join(t.TempDir(), "no-such-dir"), InitializeServerOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid root path")
}

func TestInitializeAsync_RootPath_NotDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))

	s := newTestServer()
	svc := newServerService(s)
	_, err := svc.InitializeAsync(t.Context(), file, InitializeServerOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestInitializeAsync_WithInvalidCertificate(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	svc := newServerService(s)

	badCert := "this-is-not-a-cert"
	_, err := svc.InitializeAsync(t.Context(), t.TempDir(), InitializeServerOptions{
		AuthenticationCertificate: &badCert,
	})
	require.Error(t, err)
}

// generateTestCertB64 produces a base64-DER self-signed x509 certificate. This
// is only used to exercise the certificate parsing branches in InitializeAsync
// — it is never used for real TLS.
func generateTestCertB64(t *testing.T) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(der)
}

func TestInitializeAsync_CertWithNonHttpsEndpoint(t *testing.T) {
	t.Parallel()
	cert := generateTestCertB64(t)
	endpoint := "http://example.com"

	s := newTestServer()
	svc := newServerService(s)

	_, err := svc.InitializeAsync(t.Context(), t.TempDir(), InitializeServerOptions{
		AuthenticationEndpoint:    &endpoint,
		AuthenticationCertificate: &cert,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "scheme must be 'https'")
}

func TestInitializeAsync_CertWithUnparseableEndpoint(t *testing.T) {
	t.Parallel()
	cert := generateTestCertB64(t)
	// url.Parse accepts almost anything, but a string with control chars fails.
	endpoint := "http://invalid\x7f/"

	s := newTestServer()
	svc := newServerService(s)

	_, err := svc.InitializeAsync(t.Context(), t.TempDir(), InitializeServerOptions{
		AuthenticationEndpoint:    &endpoint,
		AuthenticationCertificate: &cert,
	})
	require.Error(t, err)
}

func TestServe_ClosedListenerReturns(t *testing.T) {
	t.Setenv("AZD_DEBUG_SERVER_DEBUG_ENDPOINTS", "false")

	s := newTestServer()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- s.Serve(l)
	}()

	// Close the listener immediately; Serve should return.
	require.NoError(t, l.Close())

	select {
	case err := <-done:
		// Any error is fine; we're primarily verifying Serve returns.
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after listener close")
	}

	// cancelTelemetryUpload should have been installed.
	require.NotNil(t, s.cancelTelemetryUpload)
	s.cancelTelemetryUpload()
}

func TestValidateSession_EmptyId(t *testing.T) {
	s := newTestServer()

	_, err := s.validateSession(Session{Id: ""})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is required")
}

func TestValidateSession_InvalidId(t *testing.T) {
	s := newTestServer()

	_, err := s.validateSession(Session{Id: "does-not-exist"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestNewInfoProgressMessage_EmptyMessage(t *testing.T) {
	msg := newInfoProgressMessage("")
	require.Equal(t, "", msg.Message)
	require.Equal(t, Info, msg.Severity)
	require.Equal(t, Logging, msg.Kind)
}

func TestNewImportantProgressMessage_EmptyMessage(t *testing.T) {
	msg := newImportantProgressMessage("")
	require.Equal(t, "", msg.Message)
	require.Equal(t, Info, msg.Severity)
	require.Equal(t, Important, msg.Kind)
}

func TestProgressMessage_WithMessage_PreservesAllFields(t *testing.T) {
	original := ProgressMessage{
		Message:            "original",
		Severity:           Error,
		Kind:               Important,
		Code:               "ERR-42",
		AdditionalInfoLink: "https://docs.example.com",
	}

	updated := original.WithMessage("new message")
	require.Equal(t, "new message", updated.Message)
	require.Equal(t, Error, updated.Severity)
	require.Equal(t, Important, updated.Kind)
	require.Equal(t, "ERR-42", updated.Code)
	require.Equal(t, "https://docs.example.com", updated.AdditionalInfoLink)
	require.False(t, updated.Time.IsZero(), "Time should be set")

	// Original unchanged
	require.Equal(t, "original", original.Message)
}

func TestNewHandler_PanicRecovery_SingleReturn(t *testing.T) {
	h := NewHandler(func(ctx context.Context) error {
		panic("boom")
	})

	call, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{})
	require.NoError(t, err)

	var gotErr error
	replier := func(ctx context.Context, result any, err error) error {
		gotErr = err
		return nil
	}

	_ = h(t.Context(), nil, replier, call)
	require.Error(t, gotErr)
	require.Contains(t, gotErr.Error(), "boom")
}

func TestNewHandler_PanicRecovery_TwoReturns(t *testing.T) {
	h := NewHandler(func(ctx context.Context) (string, error) {
		panic("kaboom")
	})

	call, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "Test", []any{})
	require.NoError(t, err)

	var gotErr error
	replier := func(ctx context.Context, result any, err error) error {
		gotErr = err
		return nil
	}

	_ = h(t.Context(), nil, replier, call)
	require.Error(t, gotErr)
	require.Contains(t, gotErr.Error(), "kaboom")
}

func TestEnvironmentService_ServeHTTP(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	// Call a registered method with wrong params to exercise ServeHTTP handler map setup
	// The method exists in the handler map, so we should get InvalidParams, not MethodNotFound
	_, err := rpcConn.Call(t.Context(), "GetEnvironmentsAsync", "not-an-array", nil)
	require.Error(t, err)

	var rpcErr *jsonrpc2.Error
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, jsonrpc2.InvalidParams, rpcErr.Code)
}

func TestServerService_ServeHTTP(t *testing.T) {
	s := newTestServer()
	svc := newServerService(s)
	rpcConn := connectRPC(t, svc)

	_, err := rpcConn.Call(t.Context(), "InitializeAsync", "not-an-array", nil)
	require.Error(t, err)

	var rpcErr *jsonrpc2.Error
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, jsonrpc2.InvalidParams, rpcErr.Code)
}

func TestAspireService_ServeHTTP(t *testing.T) {
	s := newTestServer()
	svc := newAspireService(s)
	rpcConn := connectRPC(t, svc)

	_, err := rpcConn.Call(t.Context(), "GetAspireHostAsync", "not-an-array", nil)
	require.Error(t, err)

	var rpcErr *jsonrpc2.Error
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, jsonrpc2.InvalidParams, rpcErr.Code)
}

func TestMessageWriter_Write_ViaObserver(t *testing.T) {
	// The messageWriter calls observer.OnNext which sends a JSON-RPC notification.
	// We set up a websocket server to receive the notification.

	received := make(chan string, 10)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		rpcServer := jsonrpc2.NewConn(newWebSocketStream(c))
		rpcServer.Go(r.Context(), func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
			received <- req.Method()
			return nil
		})
		<-rpcServer.Done()
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	serverURL.Scheme = "ws"

	wsConn, _, err := websocket.DefaultDialer.Dial(serverURL.String(), nil)
	require.NoError(t, err)

	rpcConn := jsonrpc2.NewConn(newWebSocketStream(wsConn))
	rpcConn.Go(t.Context(), nil)

	// Create an observer with handle 5
	obs := &Observer[ProgressMessage]{
		handle: 5,
		c:      rpcConn,
	}

	// Create messageWriter
	mw := &messageWriter{
		ctx:      t.Context(),
		observer: obs,
		messageTemplate: ProgressMessage{
			Severity: Info,
			Kind:     Logging,
		},
	}

	// Write through the messageWriter
	n, err := mw.Write([]byte("test output"))
	require.NoError(t, err)
	require.Equal(t, len("test output"), n)

	// Wait for the server to receive the notification
	method := <-received
	require.Equal(t, "$/invokeProxy/5/onNext", method)
}

func TestEnvironmentService_GetEnvironmentsAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	// GetEnvironmentsAsync expects (RequestContext, Observer)
	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(t.Context(), "GetEnvironmentsAsync", []any{
		rc,
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_SetCurrentEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(t.Context(), "SetCurrentEnvironmentAsync", []any{
		rc,
		"env-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_DeleteEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(t.Context(), "DeleteEnvironmentAsync", []any{
		rc,
		"env-name",
		1,
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_CreateEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	env := Environment{
		Name: "test-env",
		Properties: map[string]string{
			"Subscription": "sub-123",
			"Location":     "eastus",
		},
	}
	_, err := rpcConn.Call(t.Context(), "CreateEnvironmentAsync", []any{
		rc,
		env,
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_OpenEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(t.Context(), "OpenEnvironmentAsync", []any{
		rc,
		"env-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_LoadEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(t.Context(), "LoadEnvironmentAsync", []any{
		rc,
		"env-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_RefreshEnvironmentAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(t.Context(), "RefreshEnvironmentAsync", []any{
		rc,
		"env-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_DeployAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(t.Context(), "DeployAsync", []any{
		rc,
		"env-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestEnvironmentService_DeployServiceAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(t.Context(), "DeployServiceAsync", []any{
		rc,
		"env-name",
		"service-name",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestAspireService_GetAspireHostAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newAspireService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(t.Context(), "GetAspireHostAsync", []any{
		rc,
		"aspire-env",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestAspireService_RenameAspireHostAsync_InvalidSession(t *testing.T) {
	s := newTestServer()
	svc := newAspireService(s)
	rpcConn := connectRPC(t, svc)

	rc := RequestContext{
		Session:         Session{Id: "bad-session"},
		HostProjectPath: "/some/path",
	}
	_, err := rpcConn.Call(t.Context(), "RenameAspireHostAsync", []any{
		rc,
		"/new/path",
		map[string]any{"__jsonrpc_marshaled": 1, "handle": 1},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.Id is invalid")
}

func TestServerService_InitializeAsync_InvalidParams(t *testing.T) {
	s := newTestServer()
	svc := newServerService(s)
	rpcConn := connectRPC(t, svc)

	// InitializeAsync expects (rootPath string, options InitializeServerOptions)
	// Use the current working directory instead of TempDir to avoid cleanup issues
	// (InitializeAsync calls os.Chdir which locks the directory on Windows)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	var result Session
	_, err = rpcConn.Call(t.Context(), "InitializeAsync", []any{
		cwd,
		InitializeServerOptions{},
	}, &result)
	// This should succeed since InitializeAsync doesn't need a pre-existing session
	require.NoError(t, err)
	require.NotEmpty(t, result.Id)

	// Restore working directory
	_ = os.Chdir(cwd)
}
