package httpapi

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"mipmi/internal/bmc"
	"mipmi/internal/hosts"
	"mipmi/internal/kvm"
	"mipmi/internal/telemetry"
	"mipmi/internal/ui"
)

// Server is the HTMX HTTP front-end.
type Server struct {
	host    *hosts.Host
	gate    *Gate
	store   *telemetry.Store
	kvm     *kvm.Bridge
	log     *slog.Logger
	tmpl    *template.Template
	mux     *http.ServeMux
	upgrader websocket.Upgrader
}

// New builds routes and templates bound to the active host.
func New(host *hosts.Host, gate *Gate, store *telemetry.Store, log *slog.Logger) (*Server, error) {
	if host == nil {
		return nil, fmt.Errorf("httpapi: nil host")
	}
	if log == nil {
		log = slog.Default()
	}
	tmpl, err := ui.ParseTemplates()
	if err != nil {
		return nil, err
	}
	bridge := kvm.NewBridge(host.Address, host.User, host.Password, host.KVMPort, host.KVMTLS, log)
	s := &Server{
		host:  host,
		gate:  gate,
		store: store,
		kvm:   bridge,
		log:   log,
		tmpl:  tmpl,
		mux:   http.NewServeMux(),
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

func (s *Server) hostID() string { return s.host.ID }

func (s *Server) displayHost() string {
	if s.host.Name != "" && s.host.Name != s.host.Address {
		return s.host.Name + " (" + s.host.Address + ")"
	}
	if s.host.Address != "" {
		return s.host.Address
	}
	return s.host.ID
}

func (s *Server) routes() {
	s.mux.Handle("/static/", ui.StaticHandler())

	s.mux.HandleFunc("GET /login", s.handleLoginGet)
	s.mux.HandleFunc("POST /login", s.handleLoginPost)
	s.mux.HandleFunc("POST /logout", s.handleLogout)
	s.mux.HandleFunc("GET /logout", s.handleLogout)

	s.mux.HandleFunc("GET /{$}", s.handleDashboard)
	s.mux.HandleFunc("GET /partials/dashboard", s.handleDashboardPartial)

	s.mux.HandleFunc("GET /power", s.handlePower)
	s.mux.HandleFunc("GET /partials/power", s.handlePowerPartial)
	s.mux.HandleFunc("POST /power", s.handlePowerAction)

	s.mux.HandleFunc("GET /sensors", s.handleSensors)
	s.mux.HandleFunc("GET /partials/sensors", s.handleSensorsPartial)

	s.mux.HandleFunc("GET /sel", s.handleSEL)
	s.mux.HandleFunc("GET /partials/sel", s.handleSELPartial)

	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /api/metrics", s.handleAPIMetrics)

	s.mux.HandleFunc("GET /console", s.handleConsole)
	s.mux.HandleFunc("GET /ws/sol", s.handleSOLWS)

	s.mux.HandleFunc("GET /kvm", s.handleKVM)
	s.mux.HandleFunc("GET /ws/kvm", s.handleKVMWS)
}

// Handler returns the root handler with auth middleware.
func (s *Server) Handler() http.Handler {
	return s.gate.Middleware(s.mux)
}

type pageData struct {
	Title   string
	Active  string
	BMCHost string
	Error   string
	Flash   string
}

func (s *Server) page(title, active string) pageData {
	return pageData{Title: title, Active: active, BMCHost: s.displayHost()}
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("template", "name", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	s.render(w, "login.html", s.page("Login", ""))
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !s.gate.validPassword(r.FormValue("password")) {
		w.WriteHeader(http.StatusUnauthorized)
		d := s.page("Login", "")
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

func (s *Server) loadDashboard() dashboardData {
	d := dashboardData{pageData: s.page("Dashboard", "dashboard")}
	meta := s.store.Meta(s.hostID())
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
		return d
	}
	d.Info = s.store.LatestMCInfo(s.hostID())
	d.Power = s.store.LatestPower(s.hostID())
	for _, sn := range s.store.LatestSensors(s.hostID()) {
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
	// Skeleton only — body loads via HTMX from the warm store.
	s.render(w, "dashboard.html", s.page("Dashboard", "dashboard"))
}

func (s *Server) handleDashboardPartial(w http.ResponseWriter, r *http.Request) {
	s.render(w, "partials/dashboard.html", s.loadDashboard())
}

type powerPageData struct {
	pageData
	Power   *bmc.PowerStatus
	ErrMsg  string
	Result  string
	Warming bool
}

func (s *Server) handlePower(w http.ResponseWriter, r *http.Request) {
	d := powerPageData{pageData: s.page("Power", "power")}
	meta := s.store.Meta(s.hostID())
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Power = s.store.LatestPower(s.hostID())
	}
	s.render(w, "power.html", d)
}

func (s *Server) handlePowerPartial(w http.ResponseWriter, r *http.Request) {
	d := powerPageData{pageData: s.page("Power", "power")}
	meta := s.store.Meta(s.hostID())
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Power = s.store.LatestPower(s.hostID())
	}
	s.render(w, "partials/power_status.html", d)
}

func (s *Server) handlePowerAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	action := bmc.PowerAction(r.FormValue("action"))
	d := powerPageData{pageData: s.page("Power", "power")}
	if err := s.host.Client.PowerControl(r.Context(), action); err != nil {
		d.ErrMsg = err.Error()
	} else {
		d.Result = fmt.Sprintf("Issued power %s", action)
	}
	// Brief settle; collector will refresh store shortly. Read live once for feedback.
	time.Sleep(400 * time.Millisecond)
	ps, err := s.host.Client.PowerStatus(r.Context())
	if err != nil && d.ErrMsg == "" {
		d.ErrMsg = err.Error()
	} else if err == nil {
		d.Power = ps
		_ = s.store.RecordPower(s.hostID(), ps)
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
	d := sensorsPageData{pageData: s.page("Sensors", "sensors")}
	meta := s.store.Meta(s.hostID())
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Sensors = s.store.LatestSensors(s.hostID())
	}
	s.render(w, "sensors.html", d)
}

func (s *Server) handleSensorsPartial(w http.ResponseWriter, r *http.Request) {
	d := sensorsPageData{pageData: s.page("Sensors", "sensors")}
	meta := s.store.Meta(s.hostID())
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Sensors = s.store.LatestSensors(s.hostID())
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
	d := selPageData{pageData: s.page("SEL", "sel")}
	meta := s.store.Meta(s.hostID())
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Entries = s.store.LatestSEL(s.hostID())
	}
	s.render(w, "sel.html", d)
}

func (s *Server) handleSELPartial(w http.ResponseWriter, r *http.Request) {
	d := selPageData{pageData: s.page("SEL", "sel")}
	meta := s.store.Meta(s.hostID())
	if meta.LastError != "" {
		d.ErrMsg = meta.LastError
	}
	if !meta.Warm {
		d.Warming = true
	} else {
		d.Entries = s.store.LatestSEL(s.hostID())
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
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "1h"
	}
	from, to := rangeWindow(rng)
	names, err := s.store.ListSensorNames(s.hostID(), from, to)
	if err != nil {
		s.log.Warn("list sensors", "err", err)
	}
	selected := r.URL.Query()["sensor"]
	if len(selected) == 0 {
		selected = defaultMetricSensors(names)
	}
	s.render(w, "metrics.html", metricsPageData{
		pageData: s.page("Metrics", "metrics"),
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
		Meta: s.store.Meta(s.hostID()),
	}

	if len(sensors) == 0 {
		names, _ := s.store.ListSensorNames(s.hostID(), from, to)
		sensors = defaultMetricSensors(names)
	}
	for _, name := range sensors {
		pts, err := s.store.QuerySamples(s.hostID(), name, from, to)
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
	Status string
	Port   int
	TLS    bool
}

func (s *Server) handleKVM(w http.ResponseWriter, r *http.Request) {
	s.render(w, "kvm.html", kvmPageData{
		pageData: s.page("KVM", "kvm"),
		Status:   s.kvm.Status(),
		Port:     s.host.KVMPort,
		TLS:      s.host.KVMTLS,
	})
}

func (s *Server) handleKVMWS(w http.ResponseWriter, r *http.Request) {
	src, sink, release, err := s.kvm.Acquire(r.Context())
	if err != nil {
		if err == kvm.ErrBusy {
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
	if err := kvm.ServeRFB(r.Context(), nc, src, sink); err != nil {
		s.log.Info("kvm rfb ended", "err", err)
	}
}

func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	s.render(w, "console.html", s.page("SOL Console", "console"))
}

func (s *Server) handleSOLWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("ws upgrade", "err", err)
		return
	}
	defer conn.Close()

	sess, err := s.host.Client.OpenSOL(r.Context())
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("\r\n*** SOL unavailable: "+err.Error()+" ***\r\n"))
		return
	}
	defer sess.Close()

	_ = conn.WriteMessage(websocket.TextMessage, []byte("\r\n*** mIPMI SOL connected to "+s.displayHost()+" (serial, not KVM) ***\r\n"))

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
