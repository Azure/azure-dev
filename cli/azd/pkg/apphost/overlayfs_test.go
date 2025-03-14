package apphost

import (
	"io"
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// In-memory mock FS for testing
type mockFS map[string][]byte

func (m mockFS) Open(name string) (fs.File, error) {
	data, ok := m[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &mockFile{data: data, name: name}, nil
}

func (m mockFS) ReadDir(name string) ([]fs.DirEntry, error) {
	// We'll consider everything in the top directory (no subdirs)
	// to keep this example simple
	if name != "." {
		return nil, fs.ErrNotExist
	}

	var entries []fs.DirEntry
	for path := range m {
		entries = append(entries, mockDirEntry{path})
	}
	if len(entries) == 0 {
		return nil, fs.ErrNotExist
	}

	return entries, nil
}

type mockFile struct {
	data []byte
	name string
	off  int
}

func (f *mockFile) Stat() (fs.FileInfo, error) { return mockFileInfo{f.name, int64(len(f.data))}, nil }
func (f *mockFile) Close() error               { return nil }
func (f *mockFile) Read(p []byte) (int, error) {
	if f.off >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.off:])
	f.off += n
	return n, nil
}

type mockFileInfo struct {
	name string
	size int64
}

func (fi mockFileInfo) Name() string       { return fi.name }
func (fi mockFileInfo) Size() int64        { return fi.size }
func (fi mockFileInfo) Mode() fs.FileMode  { return 0 }
func (fi mockFileInfo) ModTime() time.Time { return time.Time{} }
func (fi mockFileInfo) IsDir() bool        { return false }
func (fi mockFileInfo) Sys() any           { return nil }

type mockDirEntry struct {
	name string
}

func (de mockDirEntry) Name() string               { return de.name }
func (de mockDirEntry) IsDir() bool                { return false }
func (de mockDirEntry) Type() fs.FileMode          { return 0 }
func (de mockDirEntry) Info() (fs.FileInfo, error) { return mockFileInfo{de.name, 0}, nil }

// ----------------------------------------------------------------
//  The Actual Test
// ----------------------------------------------------------------

func TestOverlayFS(t *testing.T) {
	// 1) embeddedFS has foo.txt & bar.txt
	embeddedFS := mockFS{
		"foo.txt": []byte("EMBEDDED: foo"),
		"bar.txt": []byte("EMBEDDED: bar"),
	}

	// 2) localFS overrides foo.txt and has localOnly.txt
	localFS := mockFS{
		"foo.txt":       []byte("LOCAL OVERRIDE: foo"),
		"localOnly.txt": []byte("LOCAL: this file not in embedded"),
	}

	// 3) Build combined
	combined := OverlayFS(localFS, embeddedFS)

	t.Run("local file is returned when file exists", func(t *testing.T) {
		file, err := combined.Open("foo.txt")

		if err != nil {
			t.Fatalf("Open(foo.txt) error: %v", err)
		}

		content, _ := io.ReadAll(file)
		actual := string(content)

		assert.Contains(t, actual, "LOCAL OVERRIDE: foo")
	})

	t.Run("embedded file is returned when only embedded file exists", func(t *testing.T) {
		file, err := combined.Open("bar.txt")

		if err != nil {
			t.Fatalf("Open(bar.txt) error: %v", err)
		}

		content, _ := io.ReadAll(file)
		actual := string(content)

		assert.Contains(t, actual, "EMBEDDED: bar")
	})

	t.Run("read directory returns only the files that exist in the primary file system", func(t *testing.T) {
		entries, err := fs.ReadDir(combined, ".")
		if err != nil {
			t.Fatalf("ReadDir(.) error: %v", err)
		}
		assert.Len(t, entries, 2)
		assert.Contains(t, entries, mockDirEntry{"foo.txt"})
		assert.Contains(t, entries, mockDirEntry{"bar.txt"})

	})

}
