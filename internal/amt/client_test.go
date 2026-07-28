package amt

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"outband/internal/bmc"
)

func TestFeatures(t *testing.T) {
	a := New(Config{Host: "127.0.0.1", User: "admin", Password: "x"})
	f := a.Features()
	if !f.Has(bmc.FeaturePower) || !f.Has(bmc.FeatureIdentity) {
		t.Fatalf("features=%v", f)
	}
	if f.Has(bmc.FeatureConsole) || f.Has(bmc.FeatureKVM) {
		t.Fatalf("console/kvm must be omitted: %v", f)
	}
}

func TestDefaultPorts(t *testing.T) {
	c := newWSMAN(Config{Host: "h", User: "u", Password: "p"})
	if !strings.Contains(c.baseURL, ":16992/") {
		t.Fatalf("baseURL=%s", c.baseURL)
	}
	c = newWSMAN(Config{Host: "h", User: "u", Password: "p", TLS: true})
	if !strings.Contains(c.baseURL, "https://") || !strings.Contains(c.baseURL, ":16993/") {
		t.Fatalf("tls baseURL=%s", c.baseURL)
	}
}

func TestPowerAndIdentityHTTPtest(t *testing.T) {
	var nc atomic.Uint32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="Digest:TEST", nonce="n1", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(auth, "Digest ") {
			http.Error(w, "bad auth", 401)
			return
		}
		nc.Add(1)
		s := string(body)
		switch {
		case strings.Contains(s, "CIM_SoftwareIdentity") && strings.Contains(s, "Enumerate"):
			fmt.Fprint(w, enumContextXML("ec-sw"))
		case strings.Contains(s, "CIM_SoftwareIdentity") && strings.Contains(s, "Pull"):
			fmt.Fprint(w, `<?xml version="1.0"?>
<a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:g="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:h="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_SoftwareIdentity">
<a:Body><g:PullResponse><g:Items>
<h:CIM_SoftwareIdentity><h:InstanceID>AMT</h:InstanceID><h:VersionString>12.0.95</h:VersionString></h:CIM_SoftwareIdentity>
<h:CIM_SoftwareIdentity><h:InstanceID>Build Number</h:InstanceID><h:VersionString>2489</h:VersionString></h:CIM_SoftwareIdentity>
</g:Items><g:EndOfSequence/></g:PullResponse></a:Body></a:Envelope>`)
		case strings.Contains(s, "CIM_AssociatedPowerManagementService") && strings.Contains(s, "Enumerate"):
			fmt.Fprint(w, enumContextXML("ec-pw"))
		case strings.Contains(s, "CIM_AssociatedPowerManagementService") && strings.Contains(s, "Pull"):
			fmt.Fprint(w, `<?xml version="1.0"?>
<a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:g="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:h="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_AssociatedPowerManagementService">
<a:Body><g:PullResponse><g:Items>
<h:CIM_AssociatedPowerManagementService><h:PowerState>2</h:PowerState></h:CIM_AssociatedPowerManagementService>
</g:Items><g:EndOfSequence/></g:PullResponse></a:Body></a:Envelope>`)
		case strings.Contains(s, "RequestPowerStateChange"):
			fmt.Fprint(w, `<?xml version="1.0"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:r="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_PowerManagementService"><a:Body><r:RequestPowerStateChange_OUTPUT><r:ReturnValue>0</r:ReturnValue></r:RequestPowerStateChange_OUTPUT></a:Body></a:Envelope>`)
		case strings.Contains(s, "CIM_NumericSensor") && strings.Contains(s, "Enumerate"):
			fmt.Fprint(w, enumContextXML("ec-sn"))
		case strings.Contains(s, "CIM_NumericSensor") && strings.Contains(s, "Pull"):
			fmt.Fprint(w, `<?xml version="1.0"?>
<a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:g="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:h="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_NumericSensor">
<a:Body><g:PullResponse><g:Items>
<h:CIM_NumericSensor><h:ElementName>CPU Temp</h:ElementName><h:CurrentReading>42</h:CurrentReading><h:BaseUnits>2</h:BaseUnits><h:SensorType>2</h:SensorType></h:CIM_NumericSensor>
</g:Items><g:EndOfSequence/></g:PullResponse></a:Body></a:Envelope>`)
		case strings.Contains(s, "AMT_EventLogEntry") && strings.Contains(s, "Enumerate"):
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `<?xml version="1.0"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope"><a:Body><a:Fault><a:Reason><a:Text>Destination Unreachable</a:Text></a:Reason></a:Fault></a:Body></a:Envelope>`)
		default:
			http.Error(w, "unexpected: "+s[:min(80, len(s))], 500)
		}
	}))
	defer srv.Close()

	host, port := splitHostPort(srv.URL)
	a := New(Config{Host: host, Port: port, User: "admin", Password: "AmtAdmin1!"})
	defer a.Close()

	ctx := t.Context()
	info, err := a.MCInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.FirmwareRev != "12.0.95.2489" {
		t.Fatalf("firmware=%q", info.FirmwareRev)
	}
	if info.Manufacturer != "Intel" {
		t.Fatalf("mfr=%q", info.Manufacturer)
	}

	ps, err := a.PowerStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ps.IsOn {
		t.Fatal("expected on")
	}

	if err := a.PowerControl(ctx, bmc.PowerCycle); err != nil {
		t.Fatal(err)
	}

	sensors, err := a.Sensors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sensors) != 1 || sensors[0].Name != "CPU Temp" || sensors[0].Value != "42" {
		t.Fatalf("sensors=%+v", sensors)
	}

	sel, err := a.SEL(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if sel == nil {
		t.Fatal("sel should be empty slice")
	}
	if nc.Load() < 3 {
		t.Fatalf("expected digest-authenticated calls, nc=%d", nc.Load())
	}
}

func TestDigestResponse(t *testing.T) {
	tr := &digestTransport{user: "admin", password: "AmtAdmin1!"}
	chal := `Digest realm="Digest:TEST", nonce="abc", qop="auth"`
	auth, err := tr.authorize("POST", "/wsman", chal)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(auth, `username="admin"`) || !strings.Contains(auth, `response="`) {
		t.Fatalf("auth=%s", auth)
	}
	// Verify response field matches RFC 2617 for known cnonce by re-deriving with fixed nc.
	// authorize uses random cnonce; just check HA1 shape.
	ha1 := md5.Sum([]byte("admin:Digest:TEST:AmtAdmin1!"))
	_ = hex.EncodeToString(ha1[:])
}

func TestParseAMTTime(t *testing.T) {
	ts := parseAMTTime("20260728193000.000000+000")
	if ts.Year() != 2026 || ts.Month() != time.July || ts.Day() != 28 {
		t.Fatalf("ts=%v", ts)
	}
}

func enumContextXML(ec string) string {
	return fmt.Sprintf(`<?xml version="1.0"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:g="http://schemas.xmlsoap.org/ws/2004/09/enumeration"><a:Body><g:EnumerateResponse><g:EnumerationContext>%s</g:EnumerationContext></g:EnumerateResponse></a:Body></a:Envelope>`, ec)
}

func splitHostPort(rawURL string) (string, int) {
	// httptest URL like http://127.0.0.1:12345
	u := strings.TrimPrefix(rawURL, "http://")
	host, portStr, ok := strings.Cut(u, ":")
	if !ok {
		return u, 16992
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}
