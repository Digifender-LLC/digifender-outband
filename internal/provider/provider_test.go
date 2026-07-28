package provider_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"mipmi/internal/config"
	"mipmi/internal/provider"

	_ "mipmi/internal/ipmi" // register ipmi
)

func TestKnown(t *testing.T) {
	if !provider.Known("ipmi") {
		t.Fatal("ipmi should be known")
	}
	if !provider.Known("idrac") {
		t.Fatal("idrac should be known")
	}
	if provider.Known("nope") {
		t.Fatal("nope should not be known")
	}
}

func TestNewUnknown(t *testing.T) {
	_, err := provider.New(config.HostConfig{ID: "x", Provider: "nope"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("err=%v", err)
	}
}

func TestNewIdracNotImplemented(t *testing.T) {
	_, err := provider.New(config.HostConfig{ID: "d", Provider: "idrac", Host: "1.1.1.1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, provider.ErrNotImplemented) {
		t.Fatalf("err=%v want ErrNotImplemented", err)
	}
}

func TestNames(t *testing.T) {
	names := provider.Names()
	if len(names) == 0 {
		t.Fatal("Names empty")
	}
	if !slices.Contains(names, "ipmi") {
		t.Fatalf("Names=%v missing ipmi", names)
	}
	if !slices.IsSorted(names) {
		t.Fatalf("Names not sorted: %v", names)
	}
}
