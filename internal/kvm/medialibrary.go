package kvm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LibraryEntry describes an ISO in the server-side media library.
type LibraryEntry struct {
	Name    string
	Size    int64
	ModTime time.Time
}

// ListLibrary returns ISO files in mediaDir with size and modification time.
func ListLibrary(mediaDir string) ([]LibraryEntry, error) {
	dir := mediaDir
	if dir == "" {
		return nil, fmt.Errorf("media: no media directory configured")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []LibraryEntry
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		if !strings.EqualFold(filepath.Ext(name), ".iso") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, LibraryEntry{
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return out, nil
}

// DeleteLibraryISO removes a basename from the media library.
func DeleteLibraryISO(mediaDir, name string) error {
	name, err := SanitizeISOName(name)
	if err != nil {
		return err
	}
	dir := filepath.Clean(mediaDir)
	if dir == "" {
		return fmt.Errorf("media: no media directory configured")
	}
	path := filepath.Join(dir, name)
	path = filepath.Clean(path)
	if !strings.HasPrefix(path, dir+string(os.PathSeparator)) && path != dir {
		return fmt.Errorf("media: invalid ISO path")
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("media: %q not found in library", name)
		}
		return err
	}
	return nil
}
