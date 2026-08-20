package kvm

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeISOName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in    string
		want  string
		errOK bool
	}{
		{"install.iso", "install.iso", false},
		{"../evil.iso", "", true},
		{"/etc/passwd.iso", "", true},
		{"notes.txt", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		got, err := SanitizeISOName(tc.in)
		if tc.errOK {
			if err == nil {
				t.Fatalf("SanitizeISOName(%q): want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("SanitizeISOName(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("SanitizeISOName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSaveUpload(t *testing.T) {
	dir := t.TempDir()
	name, err := SaveUpload(dir, bytes.NewReader([]byte("hello iso")), "test.iso", 1024, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if name != "test.iso" {
		t.Fatalf("name = %q, want test.iso", name)
	}
	b, err := os.ReadFile(filepath.Join(dir, "test.iso"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello iso" {
		t.Fatalf("content = %q", b)
	}
	_, err = SaveUpload(dir, strings.NewReader("dup"), "test.iso", 1024, false, nil)
	if !errors.Is(err, ErrUploadExists) {
		t.Fatalf("SaveUpload: err = %v, want ErrUploadExists", err)
	}
	_, err = SaveUpload(dir, strings.NewReader("new"), "test.iso", 1024, true, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSaveUploadTooLarge(t *testing.T) {
	dir := t.TempDir()
	_, err := SaveUpload(dir, bytes.NewReader(make([]byte, 8)), "big.iso", 4, false, nil)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("SaveUpload: err = %v, want size limit error", err)
	}
}

func TestListLibrary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.iso"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := ListLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "a.iso" {
		t.Fatalf("entries = %+v", entries)
	}
}
