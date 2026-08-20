package kvm

import (
	"bytes"
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
	name, err := SaveUpload(dir, bytes.NewReader([]byte("hello iso")), "test.iso", 1024)
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
	_, err = SaveUpload(dir, strings.NewReader("dup"), "test.iso", 1024)
	if err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestSaveUploadTooLarge(t *testing.T) {
	dir := t.TempDir()
	_, err := SaveUpload(dir, bytes.NewReader(make([]byte, 8)), "big.iso", 4)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("SaveUpload: err = %v, want size limit error", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "big.iso")); !os.IsNotExist(err) {
		t.Fatal("oversized upload should not leave final file")
	}
}
