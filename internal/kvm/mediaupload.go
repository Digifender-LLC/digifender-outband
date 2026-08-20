package kvm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrUploadExists indicates the library already contains this ISO name.
var ErrUploadExists = errors.New("media: ISO already exists in library")

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
func SaveUpload(dir string, r io.Reader, name string, maxBytes int64, overwrite bool, onProgress downloadProgressFunc) (string, error) {
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
		if !overwrite {
			return "", fmt.Errorf("%w: %q", ErrUploadExists, name)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tmp := dest + ".upload"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}

	limited := io.LimitReader(r, maxBytes+1)
	src := io.Reader(limited)
	if onProgress != nil {
		onProgress(0, 0)
		src = &progressReader{r: limited, fn: onProgress}
	}

	n, copyErr := io.Copy(f, src)
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
	if onProgress != nil && n > 0 {
		onProgress(n, n)
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
