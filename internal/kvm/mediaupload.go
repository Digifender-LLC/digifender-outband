package kvm

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxUploadBytes is the largest ISO accepted via browser upload.
const MaxUploadBytes int64 = 16 << 30 // 16 GiB

// SanitizeISOName returns a safe basename for an uploaded ISO.
func SanitizeISOName(name string) (string, error) {
	raw := strings.TrimSpace(name)
	if raw == "" {
		return "", fmt.Errorf("media: invalid filename")
	}
	if strings.Contains(raw, "..") || strings.ContainsAny(raw, `/\`) {
		return "", fmt.Errorf("media: invalid filename")
	}
	name = filepath.Base(raw)
	if name == "" || name == "." {
		return "", fmt.Errorf("media: invalid filename")
	}
	if !strings.EqualFold(filepath.Ext(name), ".iso") {
		return "", fmt.Errorf("media: file must have a .iso extension")
	}
	return name, nil
}

// SaveUpload writes an uploaded ISO into dir using a safe basename.
func SaveUpload(dir string, r io.Reader, name string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		maxBytes = MaxUploadBytes
	}
	name, err := SanitizeISOName(name)
	if err != nil {
		return "", err
	}
	dir = filepath.Clean(dir)
	if dir == "" {
		return "", fmt.Errorf("media: no media directory configured")
	}
	if err := EnsureMediaDir(dir); err != nil {
		return "", err
	}

	dest := filepath.Join(dir, name)
	dest = filepath.Clean(dest)
	if !strings.HasPrefix(dest, dir+string(os.PathSeparator)) && dest != dir {
		return "", fmt.Errorf("media: upload path escapes media directory")
	}
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("media: %q already exists in library", name)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tmp := dest + ".upload"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, maxBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", closeErr
	}
	if n > maxBytes {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("media: upload exceeds %s limit", formatUploadLimit(maxBytes))
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return name, nil
}

func formatUploadLimit(n int64) string {
	if n%(1<<30) == 0 {
		return fmt.Sprintf("%d GiB", n/(1<<30))
	}
	if n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n/(1<<20))
	}
	return fmt.Sprintf("%d bytes", n)
}
