package kvm

import (
	"fmt"
	"strings"
)

// MountSource selects library file vs remote URL.
type MountSource string

const (
	MountSourceLibrary MountSource = "library"
	MountSourceURL     MountSource = "url"
)

// DeliveryMode controls how URL-backed ISOs are served.
type DeliveryMode string

const (
	DeliveryAuto   DeliveryMode = "auto"   // stream when Range works, else cache
	DeliveryStream DeliveryMode = "stream" // require HTTP Range streaming
	DeliveryCache  DeliveryMode = "cache"  // download to local cache
)

// MountRequest describes an ISO to attach as virtual CD-ROM.
type MountRequest struct {
	Source   MountSource
	Library  string       // basename under mediaDir
	URL      string       // http(s) ISO URL
	Delivery DeliveryMode // meaningful for URL mounts
}

// ParseMountRequest builds a MountRequest from form fields.
func ParseMountRequest(source, library, url, delivery string) (MountRequest, error) {
	req := MountRequest{
		Source:   MountSource(strings.TrimSpace(source)),
		Library:  strings.TrimSpace(library),
		URL:      strings.TrimSpace(url),
		Delivery: DeliveryMode(strings.TrimSpace(delivery)),
	}
	if req.Source == "" {
		req.Source = MountSourceLibrary
	}
	if req.Delivery == "" {
		req.Delivery = DeliveryAuto
	}
	switch req.Source {
	case MountSourceLibrary:
		if req.Library == "" {
			return MountRequest{}, fmt.Errorf("media: no ISO selected")
		}
	case MountSourceURL:
		if req.URL == "" {
			return MountRequest{}, fmt.Errorf("media: ISO URL is required")
		}
		switch req.Delivery {
		case DeliveryAuto, DeliveryStream, DeliveryCache:
		default:
			return MountRequest{}, fmt.Errorf("media: invalid delivery mode %q", req.Delivery)
		}
	default:
		return MountRequest{}, fmt.Errorf("media: invalid source %q", req.Source)
	}
	return req, nil
}
