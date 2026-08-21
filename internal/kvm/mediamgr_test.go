package kvm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMediaManagerResolveISO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.iso")
	if err := os.WriteFile(path, []byte{0}, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewMediaManager("192.168.9.74", "root", "pass", dir, time.Hour, nil)

	got, err := m.ResolveISO("test.iso")
	if err != nil {
		t.Fatalf("ResolveISO: %v", err)
	}
	if got != path {
		t.Fatalf("path=%q want %q", got, path)
	}

	for _, bad := range []string{"../secret.iso", "sub/x.iso", "..", ""} {
		if _, err := m.ResolveISO(bad); err == nil {
			t.Fatalf("ResolveISO(%q) expected error", bad)
		}
	}
}

func TestMountRequestFromStatus(t *testing.T) {
	got := mountRequestFromStatus(MediaStatus{
		ISO:    "install.iso",
		Source: "library",
	})
	if got.Source != MountSourceLibrary || got.Library != "install.iso" {
		t.Fatalf("library mount=%+v", got)
	}

	got = mountRequestFromStatus(MediaStatus{
		ISO:      "netboot.iso",
		Source:   "url-cache",
		URL:      "https://example.com/netboot.iso",
		Delivery: "cache",
	})
	if got.Source != MountSourceURL || got.URL == "" || got.Delivery != DeliveryCache {
		t.Fatalf("url mount=%+v", got)
	}
}

func TestMediaManagerSnapshotMount(t *testing.T) {
	m := NewMediaManager("192.168.9.74", "root", "pass", t.TempDir(), time.Hour, nil)
	if _, err := m.SnapshotMount(); err == nil {
		t.Fatal("expected error with no prior mount")
	}
	m.mu.Lock()
	m.lastMount = MountRequest{Source: MountSourceLibrary, Library: "a.iso"}
	m.mu.Unlock()
	req, err := m.SnapshotMount()
	if err != nil || req.Library != "a.iso" {
		t.Fatalf("SnapshotMount()=%+v err=%v", req, err)
	}
}

func TestMediaManagerListISOs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.iso"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewMediaManager("192.168.9.74", "root", "pass", dir, time.Hour, nil)
	isos, err := m.ListISOs()
	if err != nil {
		t.Fatal(err)
	}
	if len(isos) != 1 || isos[0] != "a.iso" {
		t.Fatalf("ListISOs()=%v want [a.iso]", isos)
	}
}
