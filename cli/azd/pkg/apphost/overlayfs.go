package apphost

import "io/fs"

// overlayFS implements fs.FS and merges local + embedded
type overlayFS struct {
	local    fs.FS
	embedded fs.FS
}

func OverlayFS(local, embedded fs.FS) fs.FS {

	return &overlayFS{
		local:    local,
		embedded: embedded,
	}
}

// overlayFS is a filesystem that combines a local filesystem with an embedded filesystem.
// When opening a file, it first tries to open it from the local filesystem
// if that fails, it falls back to the embedded filesystem.
func (o overlayFS) Open(name string) (fs.File, error) {
	// 1. Attempt to open from local
	if f, err := o.local.Open(name); err == nil {
		return f, nil
	}

	// 2. Otherwise, fall back to the embedded filesystem
	return o.embedded.Open(name)
}

func (o overlayFS) ReadDir(name string) ([]fs.DirEntry, error) {
	// 1. Read the directory from embedded
	embeddedEntries, err := fs.ReadDir(o.embedded, name)
	if err != nil {
		// If embedded doesn’t have this directory,
		// we consider it a real error
		return nil, err
	}

	// 2. Try local
	localEntries, localErr := fs.ReadDir(o.local, name)
	if localErr != nil {
		// Local folder doesn't exist? That’s okay.
		// Return the embedded entries only
		return embeddedEntries, nil
	}

	// Build a map of embedded files for quick lookups
	embedMap := make(map[string]int, len(embeddedEntries))
	for i, e := range embeddedEntries {
		embedMap[e.Name()] = i
	}

	// 3. Override any embedded entries with local
	for _, le := range localEntries {
		if idx, found := embedMap[le.Name()]; found {
			// If local has the same file name as embed, override the embedded entry
			embeddedEntries[idx] = le
		}
	}

	return embeddedEntries, nil
}
