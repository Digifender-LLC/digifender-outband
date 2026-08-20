package kvm

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"outband/internal/kvm/vmedia"
)

// ErrMediaBusy is returned when a virtual-media redirection is already active.
var ErrMediaBusy = errors.New("virtual media already mounted")

// MediaStatus describes the current CD redirection state.
type MediaStatus struct {
	Mounted  bool
	ISO      string // display label (basename or URL tail)
	Size     int64
	Source   string // library | url-stream | url-cache
	URL      string // set for URL mounts
	Delivery string // auto | stream | cache
	Err      string
}

type mediaBacking interface {
	vmedia.Reader
	Close() error
}

// MediaManager owns at most one AMI virtual-media (CD-ROM) redirection per host.
type MediaManager struct {
	host     string
	user     string
	pass     string
	mediaDir string
	cache    *MediaCache
	log      *slog.Logger

	mu     sync.Mutex
	stop   func()
	status MediaStatus
}

// NewMediaManager prepares a virtual-media controller (not yet connected).
func NewMediaManager(host, user, pass, mediaDir string, cacheTTL time.Duration, log *slog.Logger) *MediaManager {
	if log == nil {
		log = slog.Default()
	}
	cacheDir := filepath.Join(mediaDir, ".cache")
	return &MediaManager{
		host:     host,
		user:     user,
		pass:     pass,
		mediaDir: mediaDir,
		cache:    NewMediaCache(cacheDir, cacheTTL),
		log:      log,
	}
}

// MediaDir returns the configured ISO library directory.
func (m *MediaManager) MediaDir() string { return m.mediaDir }

// Status returns a snapshot of the active mount.
func (m *MediaManager) Status() MediaStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// ListISOs returns .iso basenames available under the media directory.
func (m *MediaManager) ListISOs() ([]string, error) {
	dir := m.mediaDir
	if dir == "" {
		return nil, fmt.Errorf("media: no media directory configured")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		if strings.EqualFold(filepath.Ext(name), ".iso") {
			out = append(out, name)
		}
	}
	return out, nil
}

// ResolveISO maps a user-selected basename to an absolute path inside mediaDir.
func (m *MediaManager) ResolveISO(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("media: no ISO selected")
	}
	if strings.Contains(name, string(os.PathSeparator)) || strings.Contains(name, "..") {
		return "", fmt.Errorf("media: invalid ISO name")
	}
	dir := filepath.Clean(m.mediaDir)
	if dir == "" {
		return "", fmt.Errorf("media: no media directory configured")
	}
	path := filepath.Join(dir, name)
	path = filepath.Clean(path)
	if !strings.HasPrefix(path, dir+string(os.PathSeparator)) && path != dir {
		return "", fmt.Errorf("media: ISO path escapes media directory")
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("media: %q is not a file", name)
	}
	if !strings.EqualFold(filepath.Ext(name), ".iso") {
		return "", fmt.Errorf("media: %q is not an ISO file", name)
	}
	return path, nil
}

// Mount attaches a local ISO file path as virtual CD-ROM (scripts / legacy).
func (m *MediaManager) Mount(ctx context.Context, isoPath string) error {
	if path, err := m.ResolveISO(filepath.Base(isoPath)); err == nil {
		if filepath.Clean(path) == filepath.Clean(isoPath) {
			return m.MountRequest(ctx, MountRequest{
				Source:  MountSourceLibrary,
				Library: filepath.Base(isoPath),
			})
		}
	}
	fr, err := vmedia.OpenFile(isoPath)
	if err != nil {
		return err
	}
	return m.mountBacking(ctx, fr, filepath.Base(isoPath), "library", "", "", nil)
}

// MountRequest attaches an ISO from the library or a remote URL.
func (m *MediaManager) MountRequest(ctx context.Context, req MountRequest) error {
	backing, label, source, delivery, cacheRelease, err := m.openBacking(ctx, req)
	if err != nil {
		return err
	}
	return m.mountBacking(ctx, backing, label, source, delivery, req.URL, cacheRelease)
}

func (m *MediaManager) mountBacking(ctx context.Context, backing mediaBacking, label, source, delivery, url string, cacheRelease func()) error {
	m.mu.Lock()
	if m.stop != nil {
		m.mu.Unlock()
		backing.Close()
		if cacheRelease != nil {
			cacheRelease()
		}
		return ErrMediaBusy
	}
	m.mu.Unlock()

	args, cookie, err := FetchLaunchArgs(ctx, m.host, m.user, m.pass)
	if err != nil {
		backing.Close()
		if cacheRelease != nil {
			cacheRelease()
		}
		return fmt.Errorf("media: web login: %w", err)
	}
	token := args["kvmtoken"]
	if token == "" {
		Logout(m.host, cookie)
		backing.Close()
		if cacheRelease != nil {
			cacheRelease()
		}
		return fmt.Errorf("media: no kvmtoken in jnlp")
	}

	stop, err := attachVMedia(ctx, m.host, "cd", token, args, backing)
	if err != nil {
		Logout(m.host, cookie)
		backing.Close()
		if cacheRelease != nil {
			cacheRelease()
		}
		return err
	}

	m.mu.Lock()
	m.stop = func() {
		stop()
		_ = backing.Close()
		if cacheRelease != nil {
			cacheRelease()
		}
		Logout(m.host, cookie)
	}
	m.status = MediaStatus{
		Mounted:  true,
		ISO:      label,
		Size:     backing.Size(),
		Source:   source,
		URL:      url,
		Delivery: delivery,
	}
	m.mu.Unlock()

	m.log.Info("virtual media mounted", "iso", label, "source", source, "size", backing.Size())
	return nil
}

func (m *MediaManager) openBacking(ctx context.Context, req MountRequest) (backing mediaBacking, label, source, delivery string, cacheRelease func(), err error) {
	switch req.Source {
	case MountSourceLibrary:
		path, err := m.ResolveISO(req.Library)
		if err != nil {
			return nil, "", "", "", nil, err
		}
		fr, err := vmedia.OpenFile(path)
		if err != nil {
			return nil, "", "", "", nil, err
		}
		return fr, req.Library, "library", "", nil, nil

	case MountSourceURL:
		delivery = string(req.Delivery)
		switch req.Delivery {
		case DeliveryStream:
			return m.openURLStream(ctx, req.URL)
		case DeliveryCache:
			return m.openURLCache(ctx, req.URL)
		case DeliveryAuto:
			if size, ok, probeErr := vmedia.SupportsRange(ctx, req.URL); probeErr == nil && ok && size > 0 {
				m.log.Info("media URL supports ranges; streaming", "url", req.URL)
				return m.openURLStream(ctx, req.URL)
			}
			m.log.Info("media URL does not support ranges; caching", "url", req.URL)
			return m.openURLCache(ctx, req.URL)
		default:
			return nil, "", "", "", nil, fmt.Errorf("media: invalid delivery %q", req.Delivery)
		}
	default:
		return nil, "", "", "", nil, fmt.Errorf("media: invalid source %q", req.Source)
	}
}

func (m *MediaManager) openURLStream(ctx context.Context, rawURL string) (backing mediaBacking, label, source, delivery string, cacheRelease func(), err error) {
	r, err := vmedia.OpenURL(ctx, rawURL)
	if err != nil {
		return nil, "", "", "", nil, err
	}
	return r, urlLabel(rawURL), "url-stream", "stream", nil, nil
}

func (m *MediaManager) openURLCache(ctx context.Context, rawURL string) (backing mediaBacking, label, source, delivery string, cacheRelease func(), err error) {
	path, release, err := m.cache.GetOrDownload(ctx, rawURL)
	if err != nil {
		return nil, "", "", "", nil, err
	}
	fr, err := vmedia.OpenFile(path)
	if err != nil {
		release()
		return nil, "", "", "", nil, err
	}
	return fr, urlLabel(rawURL), "url-cache", "cache", release, nil
}

func urlLabel(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		base := filepath.Base(u.Path)
		if base != "" && base != "." && base != "/" {
			return base
		}
	}
	if len(raw) > 64 {
		return raw[:61] + "..."
	}
	return raw
}

// Unmount tears down the active redirection.
func (m *MediaManager) Unmount() {
	m.mu.Lock()
	stop := m.stop
	m.stop = nil
	m.status = MediaStatus{}
	m.mu.Unlock()
	if stop != nil {
		stop()
		_ = m.cache.PurgeExpired()
		m.log.Info("virtual media unmounted")
	}
}

// EnsureMediaDir creates the ISO library directory if missing.
func EnsureMediaDir(dir string) error {
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// IsNotExist reports whether err is a missing media directory entry.
func IsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
