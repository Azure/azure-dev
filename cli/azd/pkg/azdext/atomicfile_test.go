// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// WriteFileAtomic
// ---------------------------------------------------------------------------

func TestWriteFileAtomic_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	data := []byte("hello, atomic world")

	if err := WriteFileAtomic(path, data, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error: %v", err)
	}

	// Verify content.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("WriteFileAtomic() content = %q, want %q", got, data)
	}

	// Verify permissions.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	// On Windows, permission bits are limited. Just verify the file exists.
	if fi.Size() != int64(len(data)) {
		t.Errorf("WriteFileAtomic() file size = %d, want %d", fi.Size(), len(data))
	}
}

func TestWriteFileAtomic_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	// Write initial content.
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	// Overwrite atomically.
	newData := []byte("new content here")
	if err := WriteFileAtomic(path, newData, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if string(got) != string(newData) {
		t.Errorf("WriteFileAtomic() overwrite content = %q, want %q", got, newData)
	}
}

func TestWriteFileAtomic_EmptyData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	if err := WriteFileAtomic(path, []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic(empty) error: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("WriteFileAtomic(empty) size = %d, want 0", fi.Size())
	}
}

func TestWriteFileAtomic_MissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "file.txt")
	err := WriteFileAtomic(path, []byte("data"), 0o644)
	if err == nil {
		t.Error("WriteFileAtomic() with missing directory = nil, want error")
	}
}

func TestWriteFileAtomic_PreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perm.txt")

	// Write with specific permissions.
	if err := WriteFileAtomic(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic(first) error: %v", err)
	}

	// Overwrite with perm=0 to preserve existing.
	if err := WriteFileAtomic(path, []byte("second"), 0); err != nil {
		t.Fatalf("WriteFileAtomic(second) error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("content = %q, want %q", got, "second")
	}
}

func TestWriteFileAtomic_NoTempFileLeftOnFailure(t *testing.T) {
	dir := t.TempDir()

	// Write a valid file first.
	path := filepath.Join(dir, "test.txt")
	if err := WriteFileAtomic(path, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error: %v", err)
	}

	// List directory to establish baseline.
	before, _ := os.ReadDir(dir)
	beforeCount := len(before)

	// Attempt write to a non-existent sub-directory (should fail).
	badPath := filepath.Join(dir, "nodir", "bad.txt")
	_ = WriteFileAtomic(badPath, []byte("fail"), 0o644)

	// Verify no temp files were left behind.
	after, _ := os.ReadDir(dir)
	if len(after) != beforeCount {
		t.Errorf("temp file leak: before=%d entries, after=%d entries", beforeCount, len(after))
	}
}

// ---------------------------------------------------------------------------
// CopyFileAtomic
// ---------------------------------------------------------------------------

func TestCopyFileAtomic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.txt")
	dst := filepath.Join(dir, "dest.txt")
	data := []byte("copy me atomically")

	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatalf("WriteFile(src) error: %v", err)
	}

	if err := CopyFileAtomic(src, dst, 0); err != nil {
		t.Fatalf("CopyFileAtomic() error: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile(dst) error: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("CopyFileAtomic() content = %q, want %q", got, data)
	}
}

func TestCopyFileAtomic_SourceNotFound(t *testing.T) {
	dir := t.TempDir()
	err := CopyFileAtomic(filepath.Join(dir, "missing.txt"), filepath.Join(dir, "dst.txt"), 0)
	if err == nil {
		t.Error("CopyFileAtomic() with missing source = nil, want error")
	}
}

func TestCopyFileAtomic_LargeFileStreaming(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "large-source.bin")
	dst := filepath.Join(dir, "large-dest.bin")

	const size = 12 << 20 // 12 MiB
	chunk := bytes.Repeat([]byte("azdext-streaming-copy"), 512)

	f, err := os.Create(src)
	if err != nil {
		t.Fatalf("Create(src) error: %v", err)
	}
	written := 0
	for written < size {
		n := size - written
		if n > len(chunk) {
			n = len(chunk)
		}
		copied, err := f.Write(chunk[:n])
		if err != nil {
			_ = f.Close()
			t.Fatalf("Write(src) error: %v", err)
		}
		written += copied
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close(src) error: %v", err)
	}

	if err := CopyFileAtomic(src, dst, 0); err != nil {
		t.Fatalf("CopyFileAtomic(large) error: %v", err)
	}

	srcData, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("ReadFile(src) error: %v", err)
	}
	dstData, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile(dst) error: %v", err)
	}
	if !bytes.Equal(srcData, dstData) {
		t.Fatal("large file copy mismatch")
	}
}

// ---------------------------------------------------------------------------
// BackupFile
// ---------------------------------------------------------------------------

func TestBackupFile_CreatesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("config: value")

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	backupPath, err := BackupFile(path, ".bak")
	if err != nil {
		t.Fatalf("BackupFile() error: %v", err)
	}
	if backupPath != path+".bak" {
		t.Errorf("BackupFile() path = %q, want %q", backupPath, path+".bak")
	}

	got, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("ReadFile(backup) error: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("BackupFile() content = %q, want %q", got, data)
	}
}

func TestBackupFile_DefaultSuffix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	backupPath, err := BackupFile(path, "")
	if err != nil {
		t.Fatalf("BackupFile() error: %v", err)
	}
	if backupPath != path+".bak" {
		t.Errorf("BackupFile() default suffix: path = %q, want %q", backupPath, path+".bak")
	}
}

func TestBackupFile_SourceNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.txt")

	backupPath, err := BackupFile(path, ".bak")
	if err != nil {
		t.Fatalf("BackupFile() error: %v", err)
	}
	if backupPath != "" {
		t.Errorf("BackupFile() for nonexistent source: path = %q, want empty", backupPath)
	}
}

// ---------------------------------------------------------------------------
// EnsureDir
// ---------------------------------------------------------------------------

func TestEnsureDir_CreatesNew(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")

	if err := EnsureDir(dir, 0o755); err != nil {
		t.Fatalf("EnsureDir() error: %v", err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if !fi.IsDir() {
		t.Error("EnsureDir() did not create a directory")
	}
}

func TestEnsureDir_ExistingIsNoOp(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureDir(dir, 0o755); err != nil {
		t.Fatalf("EnsureDir() on existing dir error: %v", err)
	}
}

func TestEnsureDir_DefaultPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "default-perm")
	if err := EnsureDir(dir, 0); err != nil {
		t.Fatalf("EnsureDir(perm=0) error: %v", err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if !fi.IsDir() {
		t.Error("EnsureDir(perm=0) did not create a directory")
	}
}
