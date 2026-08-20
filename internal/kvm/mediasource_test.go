package kvm

import (
	"testing"
)

func TestParseMountRequestModes(t *testing.T) {
	for _, mode := range []string{"auto", "stream", "cache"} {
		if _, err := ParseMountRequest("url", "", "https://x/y.iso", mode); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
	}
}

func TestURLLabel(t *testing.T) {
	if got := urlLabel("https://cdn.example.com/dir/install.iso"); got != "install.iso" {
		t.Fatalf("got %q", got)
	}
}
