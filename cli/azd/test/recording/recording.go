package recording

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/exp/slog"
	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

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

type Session struct {
	ProxyUrl string
}

func (s *Session) ProxyClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		return url.Parse(s.ProxyUrl)
	}
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client := &http.Client{Transport: transport}

	return client
}

func Start(t *testing.T, opts ...Options) *Session {
	opt := recordOption{}
	for _, o := range opts {
		o.Apply(&opt)
	}

	dir := callingDir(1)
	name := filepath.Join(dir, "testdata", t.Name())
	baseName := filepath.Base(name)

	if opt.mode == Unknown {
		_, err := cassette.Load(name)
		if errors.Is(err, os.ErrNotExist) {
			if os.Getenv("CI") != "" {
				t.Fatalf(
					"no recording available for %s. record this locally before re-running the pipeline",
					baseName)
			}

			t.Logf("playback not available for %s. recording locally", baseName)
			opt.mode = Record
		} else if err != nil {
			t.Fatalf("failed to load cassette: %v", err)
		} else {
			opt.mode = Playback
		}
	}

	writer := &logWriter{t: t}
	log := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	var cst *cassette.Cassette
	var isPlayback bool
	var modeStr string

	switch opt.mode {
	case Live:
		return nil
	case Playback:
		var err error
		cst, err = cassette.Load(name)
		if err != nil {
			t.Fatalf("failed to load cassette: %v", err)
		}
		isPlayback = true
		modeStr = "playback"
	case Record:
		cst = cassette.New(name)
		isPlayback = false
		modeStr = "record"
	}

	if opt.mode != Playback && opt.mode != Record {
		t.Fatalf("invalid record mode: %v", opt.mode)
	}

	proxy := &connectHandler{
		Log: log,
		HttpHandler: &recorderProxy{
			Log: log,
			Panic: func(msg string) {
				t.Fatal(msg)
			},
			Playback: isPlayback,
			Cst:      cst,
		},
	}

	server := httptest.NewTLSServer(proxy)
	proxy.TLS = server.TLS
	t.Logf("recordingProxy started with mode %s at %s", modeStr, server.URL)
	t.Cleanup(func() {
		server.Close()
		if !t.Failed() {
			cst.Name = cst.Name + ".failed"
			if err := cst.Save(); err != nil {
				t.Errorf("failed to save recording: %v", err)
			}
		}
	})

	return &Session{
		ProxyUrl: server.URL,
	}
}

func callingDir(skip int) string {
	_, b, _, _ := runtime.Caller(skip + 1)
	return filepath.Dir(b)
}

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
