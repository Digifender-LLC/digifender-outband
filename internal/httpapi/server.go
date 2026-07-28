package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"outband/internal/amt/redir"
	"outband/internal/bmc"
	"outband/internal/config"
	"outband/internal/hosts"
	"outband/internal/ilo/rc"
	"outband/internal/kvm"
	"outband/internal/rfb"
	"outband/internal/telemetry"
	"outband/internal/ui"
)

// kvmBridge is the AMI, AMT, or iLO KVM session owner used by /ws/kvm.
type kvmBridge interface {
	Acquire(ctx context.Context) (rfb.Source, rfb.Sink, func(), error)
	Status() string
}

// Server is the HTMX HTTP front-end.
type Server struct {
	registry  *hosts.Registry
	defaultID string
	kvms      map[string]kvmBridge
	gate      *Gate
	oidc      *oidcAuth
	store     *telemetry.Store
	log       *slog.Logger
	tmpl      *template.Template
	mux       *http.ServeMux
	upgrader  websocket.Upgrader
}

// New builds routes and templates bound to the host registry.
// oidcCfg may be zero; when enabled, discovery runs during construction.
func New(registry *hosts.Registry, gate *Gate, store *telemetry.Store, log *slog.Logger, oidcCfg config.OIDCConfig) (*Server, error) {
	if registry == nil || len(registry.All()) == 0 {
		return nil, fmt.Errorf("httpapi: empty host registry")
	}
	if log == nil {
		log = slog.Default()
	}
	tmpl, err := ui.ParseTemplates()
	if err != nil {
		return nil, err
	}
	kvms := make(map[string]kvmBridge)
	for _, h := range registry.All() {
		switch {
		case h.HasAMTKVM():
			wsPort := h.Port
			wsTLS := h.AMTTLS()
			kvms[h.ID] = redir.NewBridge(h.Address, h.User, h.Password, h.KVMPort(), h.KVMTLS(), wsPort, wsTLS, log)
		case h.HasAMIKVM():
			kvms[h.ID] = kvm.NewBridge(h.Address, h.User, h.Password, h.KVMPort(), h.KVMTLS(), log)
		case h.HasILOKVM():
			kvms[h.ID] = rc.NewBridge(h.Address, h.Port, h.User, h.Password, h.ILOInsecureSkipVerify(), log)
		}
	}
	oa, err := newOIDCAuth(context.Background(), oidcCfg)
	if err != nil {
		return nil, err
	}
	s := &Server{
		registry:  registry,
		defaultID: registry.DefaultID(),
		kvms:      kvms,
		gate:      gate,
		oidc:      oa,
		store:     store,
		log:       log,
		tmpl:      tmpl,
		mux:       http.NewServeMux(),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			Subprotocols:    []string{"binary"},
			CheckOrigin: func(r *http.Request) bool {
				return true // LAN tool; expect reverse proxy or trusted LAN
			},
		},
	}
	s.routes()
	return s, nil
}

func featuresFor(h *hosts.Host) bmc.FeatureSet {
	return h.Features()
}

func displayHost(h *hosts.Host) string {
	if h.Name != "" && h.Name != h.Address {
		return h.Name + " (" + h.Address + ")"
	}
	if h.Address != "" {
		return h.Address
	}
	return h.ID
}

func hostBase(id string) string {
	return "/h/" + id
}

func (s *Server) resolveHost(w http.ResponseWriter, r *http.Request) (*hosts.Host, bool) {
	id := r.PathValue("hostID")
	h, err := s.registry.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	return h, true
}

func (s *Server) routes() {
	s.mux.Handle("/static/", ui.StaticHandler())

	s.mux.HandleFunc("GET /login", s.handleLoginGet)
	s.mux.HandleFunc("POST /login", s.handleLoginPost)
	s.mux.HandleFunc("POST /logout", s.handleLogout)
	s.mux.HandleFunc("GET /logout", s.handleLogout)
	s.mux.HandleFunc("GET /auth/oidc/login", s.handleOIDCLogin)
	s.mux.HandleFunc("GET /auth/oidc/callback", s.handleOIDCCallback)

	// Default host landing + legacy unprefixed paths.
	s.mux.HandleFunc("GET /{$}", s.redirectDefault(""))
	for _, suffix := range []string{
		"/power", "/partials/power",
		"/sensors", "/partials/sensors",
		"/sel", "/partials/sel",
		"/metrics", "/api/metrics",
		"/partials/dashboard",
		"/console", "/kvm",
		"/ws/sol", "/ws/kvm",
	} {
		s.mux.HandleFunc("GET "+suffix, s.redirectDefault(suffix))
	}
	s.mux.HandleFunc("POST /power", s.redirectDefault("/power"))

	const p = "/h/{hostID}"
	s.mux.HandleFunc("GET "+p+"/{$}", s.handleDashboard)
	s.mux.HandleFunc("GET "+p, s.handleDashboard) // /h/{id} without trailing slash
	s.mux.HandleFunc("GET "+p+"/partials/dashboard", s.handleDashboardPartial)

	s.mux.HandleFunc("GET "+p+"/power", s.handlePower)
	s.mux.HandleFunc("GET "+p+"/partials/power", s.handlePowerPartial)
	s.mux.HandleFunc("POST "+p+"/power", s.handlePowerAction)

	s.mux.HandleFunc("GET "+p+"/sensors", s.handleSensors)
	s.mux.HandleFunc("GET "+p+"/partials/sensors", s.handleSensorsPartial)

	s.mux.HandleFunc("GET "+p+"/sel", s.handleSEL)
	s.mux.HandleFunc("GET "+p+"/partials/sel", s.handleSELPartial)

	s.mux.HandleFunc("GET "+p+"/metrics", s.handleMetrics)
	s.mux.HandleFunc("GET "+p+"/api/metrics", s.handleAPIMetrics)

	s.mux.HandleFunc("GET "+p+"/console", s.handleConsole)
	s.mux.HandleFunc("GET "+p+"/ws/sol", s.handleSOLWS)

	s.mux.HandleFunc("GET "+p+"/kvm", s.handleKVM)
	s.mux.HandleFunc("GET "+p+"/ws/kvm", s.handleKVMWS)
}

func (s *Server) redirectDefault(suffix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := hostBase(s.defaultID) + "/"
		if suffix != "" {
			target = hostBase(s.defaultID) + suffix
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	}
}

// Handler returns the root handler with auth middleware.
func (s *Server) Handler() http.Handler {
	return s.gate.Middleware(s.mux)
}

type hostOption struct {
	ID       string
	Label    string
	Provider string
}

type pageData struct {
	Title           string
	Active          string
	BMCHost         string
	HostID          string
	HostBase        string
	Hosts           []hostOption
	Error           string
	Flash           string
	OIDCEnabled     bool
	PasswordEnabled bool
	ShowPower       bool
	ShowSensors     bool
	ShowSEL         bool
	ShowConsole     bool
	ShowKVM         bool
}

func (s *Server) page(h *hosts.Host, title, active string) pageData {
	features := featuresFor(h)
	opts := make([]hostOption, 0, len(s.registry.All()))
	for _, oh := range s.registry.All() {
		opts = append(opts, hostOption{
			ID:       oh.ID,
			Label:    displayHost(oh),
			Provider: oh.Provider,
		})
	}
	return pageData{
		Title:           title,
		Active:          active,
		BMCHost:         displayHost(h),
		HostID:          h.ID,
		HostBase:        hostBase(h.ID),
		Hosts:           opts,
		OIDCEnabled:     s.oidc != nil,
		PasswordEnabled: s.gate != nil && s.gate.passwordEnabled(),
		ShowPower:       features.Has(bmc.FeaturePower),
		ShowSensors:     features.Has(bmc.FeatureSensors),
		ShowSEL:         features.Has(bmc.FeatureSEL),
		ShowConsole:     features.Has(bmc.FeatureConsole),
		ShowKVM:         features.Has(bmc.FeatureKVM),
	}
}

func (s *Server) requireFeature(w http.ResponseWriter, h *hosts.Host, f bmc.Feature, label string) bool {
	if featuresFor(h).Has(f) {
		return true
	}
	http.Error(w, label+" not supported by this BMC", http.StatusNotImplemented)
	return false
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("template", "name", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	// Login has no selected host; use default for branding fields only.
	h := s.registry.Default()
	s.render(w, "login.html", s.page(h, "Login", ""))
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if !s.gate.passwordEnabled() {
		http.Error(w, "password login disabled", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !s.gate.validPassword(r.FormValue("password")) {
		w.WriteHeader(http.StatusUnauthorized)
		d := s.page(s.registry.Default(), "Login", "")
		d.Error = "Invalid password"
		s.render(w, "login.html", d)
		return
	}
	token, exp := s.gate.issueToken()
	s.gate.setSessionCookie(w, token, exp)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.gate.revoke(c.Value)
	}
	s.gate.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

type dashboardData struct {
	pageData
	Info    *bmc.MCInfo
	Power   *bmc.PowerStatus
	Temps   []bmc.Sensor
	Fans    []bmc.Sensor
	ErrMsg  string
	Warming bool
}

func (s *Server) loadDashboard(h *hosts.Host) dashboardData {
	d := dashboardData{pageData: s.page(h, "Dashboard", "dashboard")}
	meta := s.store.Meta(h.ID)
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
		return d
	}
	d.Info = s.store.LatestMCInfo(h.ID)
	d.Power = s.store.LatestPower(h.ID)
	for _, sn := range s.store.LatestSensors(h.ID) {
		t := strings.ToLower(sn.Type)
		if strings.Contains(t, "temperature") && sn.Present && sn.Status == "ok" {
			d.Temps = append(d.Temps, sn)
		}
		if strings.Contains(t, "fan") && sn.Present && sn.Status == "ok" {
			d.Fans = append(d.Fans, sn)
		}
	}
	if len(d.Temps) > 6 {
		d.Temps = d.Temps[:6]
	}
	if len(d.Fans) > 6 {
		d.Fans = d.Fans[:6]
	}
	return d
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	// Skeleton only — body loads via HTMX from the warm store.
	s.render(w, "dashboard.html", s.page(h, "Dashboard", "dashboard"))
}

func (s *Server) handleDashboardPartial(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	s.render(w, "partials/dashboard.html", s.loadDashboard(h))
}

type powerPageData struct {
	pageData
	Power   *bmc.PowerStatus
	ErrMsg  string
	Result  string
	Warming bool
}

func (s *Server) handlePower(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeaturePower, "Power") {
		return
	}
	d := powerPageData{pageData: s.page(h, "Power", "power")}
	meta := s.store.Meta(h.ID)
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Power = s.store.LatestPower(h.ID)
	}
	s.render(w, "power.html", d)
}

func (s *Server) handlePowerPartial(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeaturePower, "Power") {
		return
	}
	d := powerPageData{pageData: s.page(h, "Power", "power")}
	meta := s.store.Meta(h.ID)
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Power = s.store.LatestPower(h.ID)
	}
	s.render(w, "partials/power_status.html", d)
}

func (s *Server) handlePowerAction(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeaturePower, "Power") {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	action := bmc.PowerAction(r.FormValue("action"))
	d := powerPageData{pageData: s.page(h, "Power", "power")}
	if err := h.Client.PowerControl(r.Context(), action); err != nil {
		d.ErrMsg = err.Error()
	} else {
		d.Result = fmt.Sprintf("Issued power %s", action)
	}
	// Brief settle; collector will refresh store shortly. Read live once for feedback.
	time.Sleep(400 * time.Millisecond)
	ps, err := h.Client.PowerStatus(r.Context())
	if err != nil && d.ErrMsg == "" {
		d.ErrMsg = err.Error()
	} else if err == nil {
		d.Power = ps
		_ = s.store.RecordPower(h.ID, ps)
	}
	s.render(w, "partials/power_panel.html", d)
}

type sensorsPageData struct {
	pageData
	Sensors []bmc.Sensor
	ErrMsg  string
	Warming bool
}

func (s *Server) handleSensors(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeatureSensors, "Sensors") {
		return
	}
	d := sensorsPageData{pageData: s.page(h, "Sensors", "sensors")}
	meta := s.store.Meta(h.ID)
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Sensors = s.store.LatestSensors(h.ID)
	}
	s.render(w, "sensors.html", d)
}

func (s *Server) handleSensorsPartial(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeatureSensors, "Sensors") {
		return
	}
	d := sensorsPageData{pageData: s.page(h, "Sensors", "sensors")}
	meta := s.store.Meta(h.ID)
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Sensors = s.store.LatestSensors(h.ID)
	}
	s.render(w, "partials/sensors.html", d)
}

type selPageData struct {
	pageData
	Entries []bmc.SELEntry
	ErrMsg  string
	Warming bool
}

func (s *Server) handleSEL(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeatureSEL, "SEL") {
		return
	}
	d := selPageData{pageData: s.page(h, "SEL", "sel")}
	meta := s.store.Meta(h.ID)
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Entries = s.store.LatestSEL(h.ID)
	}
	s.render(w, "sel.html", d)
}

func (s *Server) handleSELPartial(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeatureSEL, "SEL") {
		return
	}
	d := selPageData{pageData: s.page(h, "SEL", "sel")}
	meta := s.store.Meta(h.ID)
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Entries = s.store.LatestSEL(h.ID)
	}
	s.render(w, "partials/sel.html", d)
}

type metricsPageData struct {
	pageData
	Sensors  []string
	Range    string
	Selected []string
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeatureSensors, "Metrics") {
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "1h"
	}
	from, to := rangeWindow(rng)
	names, err := s.store.ListSensorNames(h.ID, from, to)
	if err != nil {
		s.log.Warn("list sensors", "err", err)
	}
	selected := r.URL.Query()["sensor"]
	if len(selected) == 0 {
		selected = defaultMetricSensors(names)
	}
	s.render(w, "metrics.html", metricsPageData{
		pageData: s.page(h, "Metrics", "metrics"),
		Sensors:  names,
		Range:    rng,
		Selected: selected,
	})
}

func rangeWindow(rng string) (from, to int64) {
	to = time.Now().Unix()
	switch rng {
	case "15m":
		from = to - 15*60
	case "6h":
		from = to - 6*3600
	case "24h":
		from = to - 24*3600
	default: // 1h
		from = to - 3600
	}
	return from, to
}

func defaultMetricSensors(names []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	for _, n := range names {
		low := strings.ToLower(n)
		if strings.Contains(low, "temp") || strings.Contains(low, "cpu") ||
			strings.Contains(low, "fan") || strings.Contains(low, "inlet") ||
			strings.Contains(low, "ambient") || strings.Contains(low, "dimm") ||
			strings.Contains(low, "sys.") {
			add(n)
		}
		if len(out) >= 8 {
			return out
		}
	}
	for _, n := range names {
		add(n)
		if len(out) >= 6 {
			break
		}
	}
	return out
}

func (s *Server) handleAPIMetrics(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeatureSensors, "Metrics") {
		return
	}
	q := r.URL.Query()
	sensors := q["sensor"]
	if len(sensors) == 0 {
		if one := q.Get("sensor"); one != "" {
			sensors = []string{one}
		}
	}
	rng := q.Get("range")
	if rng == "" {
		rng = "1h"
	}
	from, to := rangeWindow(rng)
	if v := q.Get("from"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			from = n
		}
	}
	if v := q.Get("to"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			to = n
		}
	}

	type series struct {
		Sensor string             `json:"sensor"`
		Kind   string             `json:"kind"`
		Unit   string             `json:"unit"`
		Points []telemetry.Sample `json:"points"`
	}
	resp := struct {
		From   int64          `json:"from"`
		To     int64          `json:"to"`
		Series []series       `json:"series"`
		Meta   telemetry.Meta `json:"meta"`
	}{
		From: from,
		To:   to,
		Meta: s.store.Meta(h.ID),
	}

	if len(sensors) == 0 {
		names, _ := s.store.ListSensorNames(h.ID, from, to)
		sensors = defaultMetricSensors(names)
	}
	for _, name := range sensors {
		pts, err := s.store.QuerySamples(h.ID, name, from, to)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		kind, unit := "", ""
		if len(pts) > 0 {
			kind = pts[0].Kind
			unit = pts[0].Unit
		}
		resp.Series = append(resp.Series, series{Sensor: name, Kind: kind, Unit: unit, Points: pts})
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(resp)
}

type kvmPageData struct {
	pageData
	Status  string
	Backend string
	Port    int
	TLS     bool
	WSPath  string
}

func (s *Server) handleKVM(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeatureKVM, "KVM") {
		return
	}
	status := "unavailable"
	bridge := s.kvms[h.ID]
	if bridge != nil {
		status = bridge.Status()
	}
	backend := "KVM"
	switch {
	case h.HasAMTKVM():
		backend = "Intel AMT Hardware-KVM"
	case h.HasAMIKVM():
		backend = "AMI Adviser/IVTP"
	case h.HasILOKVM():
		backend = "HPE iLO IRC"
	}
	// noVNC path is origin-relative without a leading slash.
	wsPath := "h/" + h.ID + "/ws/kvm"
	s.render(w, "kvm.html", kvmPageData{
		pageData: s.page(h, "KVM", "kvm"),
		Status:   status,
		Backend:  backend,
		Port:     h.KVMPort(),
		TLS:      h.KVMTLS(),
		WSPath:   wsPath,
	})
}

func (s *Server) handleKVMWS(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeatureKVM, "KVM") {
		return
	}
	bridge := s.kvms[h.ID]
	if bridge == nil {
		http.Error(w, "KVM not supported by this BMC", http.StatusNotImplemented)
		return
	}
	src, sink, release, err := bridge.Acquire(r.Context())
	if err != nil {
		if errors.Is(err, kvm.ErrBusy) || errors.Is(err, redir.ErrBusy) || errors.Is(err, rc.ErrBusy) {
			http.Error(w, "KVM session busy", http.StatusConflict)
			return
		}
		s.log.Error("kvm acquire", "err", err)
		http.Error(w, "KVM connect failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer release()

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("kvm ws upgrade", "err", err)
		return
	}
	defer conn.Close()

	nc := newWSNetConn(conn)
	if err := rfb.Serve(r.Context(), nc, src, sink); err != nil {
		s.log.Info("kvm rfb ended", "err", err)
	}
}

type consolePageData struct {
	pageData
	WSURL string
}

func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeatureConsole, "Console") {
		return
	}
	s.render(w, "console.html", consolePageData{
		pageData: s.page(h, "SOL Console", "console"),
		WSURL:    hostBase(h.ID) + "/ws/sol",
	})
}

func (s *Server) handleSOLWS(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resolveHost(w, r)
	if !ok {
		return
	}
	if !s.requireFeature(w, h, bmc.FeatureConsole, "Console") {
		return
	}
	console, hasConsole := bmc.AsConsole(h.Client)
	if !hasConsole {
		http.Error(w, "Console not supported by this BMC", http.StatusNotImplemented)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("ws upgrade", "err", err)
		return
	}
	defer conn.Close()

	sess, err := console.OpenSOL(r.Context())
	if err != nil {
		msg := "SOL unavailable: " + err.Error()
		if errors.Is(err, bmc.ErrBusy) {
			msg = "SOL session busy"
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte("\r\n*** "+msg+" ***\r\n"))
		return
	}
	defer sess.Close()

	_ = conn.WriteMessage(websocket.TextMessage, []byte("\r\n*** Outband SOL connected to "+displayHost(h)+" (serial, not KVM) ***\r\n"))

	errCh := make(chan error, 2)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					errCh <- werr
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					errCh <- err
				} else {
					errCh <- io.EOF
				}
				return
			}
		}
	}()

	go func() {
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if mt == websocket.TextMessage || mt == websocket.BinaryMessage {
				if _, err := sess.Write(data); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	<-errCh
}
