package idrac

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"outband/internal/bmc"
)

const (
	dellNS = "http://schemas.dell.com/wbem/wscim/1/cim-schema/2/"

	uriDCIMSystemView = dellNS + "DCIM_SystemView"
	uriDCIMComputer   = dellNS + "DCIM_ComputerSystem"
	uriDCIMNumeric    = dellNS + "DCIM_NumericSensor"
	uriDCIMSEL        = dellNS + "DCIM_SELLogEntry"

	actionEnumerate = "http://schemas.xmlsoap.org/ws/2004/09/enumeration/Enumerate"
	actionPull      = "http://schemas.xmlsoap.org/ws/2004/09/enumeration/Pull"
	actionInvokeRS  = dellNS + "DCIM_ComputerSystem/RequestStateChange"
	addrAnonymous   = "http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous"
)

// wsmanBackend talks Dell iDRAC WS-MAN with HTTP Basic (OPENWSMAN realm).
type wsmanBackend struct {
	cfg     Config
	baseURL string
	http    *http.Client
	msgID   atomic.Uint64
}

func newWSMANBackend(cfg Config, useLegacyTLS bool) *wsmanBackend {
	tlsCfg := modernTLS(cfg.InsecureSkipVerify)
	if useLegacyTLS {
		tlsCfg = legacyTLS(cfg.InsecureSkipVerify)
	}
	return &wsmanBackend{
		cfg:     cfg,
		baseURL: baseURL(cfg) + "/wsman",
		http:    newHTTPClient(tlsCfg, false),
	}
}

func (c *wsmanBackend) Name() string { return TransportWSMAN }

func (c *wsmanBackend) Close() error {
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
	return nil
}

func (c *wsmanBackend) nextMsgID() string {
	return fmt.Sprintf("uuid:%d", c.msgID.Add(1))
}

func (c *wsmanBackend) post(ctx context.Context, body string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.cfg.User, c.cfg.Password)
	req.Header.Set("Content-Type", "application/soap+xml;charset=UTF-8")
	req.Header.Set("User-Agent", "outband-idrac")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("idrac wsman HTTP %d: %s", resp.StatusCode, truncateErr(data))
	}
	if hasSOAPFault(data) {
		if reason := firstLocalText(data, "Text"); reason != "" {
			return data, fmt.Errorf("idrac wsman fault: %s", reason)
		}
		return data, fmt.Errorf("idrac wsman SOAP fault")
	}
	return data, nil
}

func (c *wsmanBackend) enumeratePull(ctx context.Context, resourceURI string) ([]byte, error) {
	enumBody := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns:n="http://schemas.xmlsoap.org/ws/2004/09/enumeration">`+
		`<s:Header>`+
		`<a:Action>%s</a:Action><a:To>%s</a:To>`+
		`<w:ResourceURI s:mustUnderstand="true">%s</w:ResourceURI>`+
		`<a:MessageID>%s</a:MessageID>`+
		`<a:ReplyTo><a:Address>%s</a:Address></a:ReplyTo>`+
		`<w:SelectorSet><w:Selector Name="__cimnamespace">root/dcim</w:Selector></w:SelectorSet>`+
		`</s:Header>`+
		`<s:Body><n:Enumerate/></s:Body></s:Envelope>`,
		actionEnumerate, xmlEscapeAttr(c.baseURL), resourceURI, c.nextMsgID(), addrAnonymous)

	enumResp, err := c.post(ctx, enumBody)
	if err != nil {
		return nil, err
	}
	ec := firstLocalText(enumResp, "EnumerationContext")
	if ec == "" {
		return nil, fmt.Errorf("idrac wsman: missing EnumerationContext")
	}
	pullBody := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns:n="http://schemas.xmlsoap.org/ws/2004/09/enumeration">`+
		`<s:Header>`+
		`<a:Action>%s</a:Action><a:To>%s</a:To>`+
		`<w:ResourceURI s:mustUnderstand="true">%s</w:ResourceURI>`+
		`<a:MessageID>%s</a:MessageID>`+
		`<a:ReplyTo><a:Address>%s</a:Address></a:ReplyTo>`+
		`<w:SelectorSet><w:Selector Name="__cimnamespace">root/dcim</w:Selector></w:SelectorSet>`+
		`</s:Header>`+
		`<s:Body><n:Pull><n:EnumerationContext>%s</n:EnumerationContext><n:MaxElements>999</n:MaxElements></n:Pull></s:Body></s:Envelope>`,
		actionPull, xmlEscapeAttr(c.baseURL), resourceURI, c.nextMsgID(), addrAnonymous, xmlEscape(ec))
	return c.post(ctx, pullBody)
}

func (c *wsmanBackend) MCInfo(ctx context.Context) (*bmc.MCInfo, error) {
	info := &bmc.MCInfo{
		Manufacturer:    "Dell",
		Model:           "iDRAC",
		ProtocolVersion: "WS-MAN",
		FirmwareRev:     "unknown",
	}
	data, err := c.enumeratePull(ctx, uriDCIMSystemView)
	if err != nil {
		return nil, err
	}
	items := eachNamedElement(data, "DCIM_SystemView")
	if len(items) == 0 {
		items = eachNamedElement(data, "SystemView")
	}
	for _, it := range items {
		if v := it["Model"]; v != "" {
			info.Model = v
		}
		if v := it["Manufacturer"]; v != "" {
			info.Manufacturer = v
		}
		if v := it["LifecycleControllerVersion"]; v != "" {
			info.FirmwareRev = v
		}
		if v := it["SystemBIOSVersion"]; v != "" && info.FirmwareRev == "unknown" {
			info.FirmwareRev = "BIOS " + v
		}
	}
	return info, nil
}

func (c *wsmanBackend) PowerStatus(ctx context.Context) (*bmc.PowerStatus, error) {
	data, err := c.enumeratePull(ctx, uriDCIMComputer)
	if err != nil {
		return nil, err
	}
	items := eachNamedElement(data, "DCIM_ComputerSystem")
	for _, it := range items {
		name := it["Name"]
		// Host system is typically srv:system / System.Embedded.1
		if name != "" && !strings.Contains(strings.ToLower(name), "system") && name != "srv:system" {
			continue
		}
		ps := it["PowerState"]
		if ps == "" {
			ps = it["EnabledState"]
		}
		switch ps {
		case "2", "On", "on":
			return &bmc.PowerStatus{IsOn: true}, nil
		case "8", "7", "3", "Off", "off", "6":
			return &bmc.PowerStatus{IsOn: false}, nil
		}
	}
	// Fallback: any PowerState in response.
	if ps := firstLocalText(data, "PowerState"); ps != "" {
		return &bmc.PowerStatus{IsOn: ps == "2" || strings.EqualFold(ps, "On")}, nil
	}
	return nil, fmt.Errorf("idrac wsman: power state not found")
}

func (c *wsmanBackend) PowerControl(ctx context.Context, action bmc.PowerAction) error {
	// RequestedState on DCIM_ComputerSystem: 2=On, 8=Off, 11=Reset (warm), 5=Power Cycle Soft-ish.
	var state int
	switch action {
	case bmc.PowerOn:
		state = 2
	case bmc.PowerOff:
		state = 8
	case bmc.PowerCycle:
		state = 11
	case bmc.PowerSoft:
		state = 5
	default:
		return fmt.Errorf("%w: power action %q", bmc.ErrUnsupported, action)
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns:p="%s">`+
		`<s:Header>`+
		`<a:Action>%s</a:Action><a:To>%s</a:To>`+
		`<w:ResourceURI s:mustUnderstand="true">%s</w:ResourceURI>`+
		`<a:MessageID>%s</a:MessageID>`+
		`<a:ReplyTo><a:Address>%s</a:Address></a:ReplyTo>`+
		`<w:SelectorSet>`+
		`<w:Selector Name="__cimnamespace">root/dcim</w:Selector>`+
		`<w:Selector Name="CreationClassName">DCIM_ComputerSystem</w:Selector>`+
		`<w:Selector Name="Name">srv:system</w:Selector>`+
		`</w:SelectorSet>`+
		`</s:Header>`+
		`<s:Body><p:RequestStateChange_INPUT><p:RequestedState>%d</p:RequestedState></p:RequestStateChange_INPUT></s:Body>`+
		`</s:Envelope>`,
		uriDCIMComputer, actionInvokeRS, xmlEscapeAttr(c.baseURL), uriDCIMComputer, c.nextMsgID(), addrAnonymous, state)
	data, err := c.post(ctx, body)
	if err != nil {
		return err
	}
	if rc := firstLocalText(data, "ReturnValue"); rc != "" && rc != "0" {
		return fmt.Errorf("idrac wsman RequestStateChange ReturnValue=%s", rc)
	}
	return nil
}

func (c *wsmanBackend) Sensors(ctx context.Context) ([]bmc.Sensor, error) {
	data, err := c.enumeratePull(ctx, uriDCIMNumeric)
	if err != nil {
		return nil, err
	}
	items := eachNamedElement(data, "DCIM_NumericSensor")
	if len(items) == 0 {
		items = eachNamedElement(data, "CIM_NumericSensor")
	}
	out := make([]bmc.Sensor, 0, len(items))
	for i, it := range items {
		name := it["ElementName"]
		if name == "" {
			name = it["Name"]
		}
		if name == "" {
			name = fmt.Sprintf("Sensor-%d", i)
		}
		id := it["DeviceID"]
		if id == "" {
			id = it["InstanceID"]
		}
		if id == "" {
			id = fmt.Sprintf("sensor-%d", i)
		}
		val := it["CurrentReading"]
		if val == "" {
			val = it["Reading"]
		}
		unit := it["BaseUnits"]
		if unit == "" {
			unit = it["UnitModifier"]
		}
		typ := it["SensorType"]
		if typ == "" {
			typ = "Sensor"
		}
		status := it["HealthState"]
		if status == "" {
			status = it["OperationalStatus"]
		}
		if status == "" {
			status = "ok"
		}
		out = append(out, bmc.Sensor{
			ID: id, Name: name, Type: typ, Value: val, Unit: unit,
			Status: status, Present: true,
		})
	}
	return out, nil
}

func (c *wsmanBackend) SEL(ctx context.Context, limit int) ([]bmc.SELEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	data, err := c.enumeratePull(ctx, uriDCIMSEL)
	if err != nil {
		return nil, err
	}
	items := eachNamedElement(data, "DCIM_SELLogEntry")
	entries := make([]bmc.SELEntry, 0, len(items))
	for _, it := range items {
		id := it["RecordID"]
		if id == "" {
			id = it["InstanceID"]
		}
		msg := it["Message"]
		if msg == "" {
			msg = it["Description"]
		}
		if id == "" && msg == "" {
			continue
		}
		ts := parseDellTimestamp(it["CreationTimeStamp"])
		if ts.IsZero() {
			ts = parseRedfishTime(it["CreationTimeStamp"])
		}
		entries = append(entries, bmc.SELEntry{
			ID: id, Timestamp: ts,
			SensorType: it["SensorType"], SensorName: it["SensorName"],
			Description: msg, Severity: it["PerceivedSeverity"],
		})
	}
	sortSELDesc(entries)
	return truncateSEL(entries, limit), nil
}

func parseDellTimestamp(s string) time.Time {
	// e.g. 20200728123045.000000+000
	s = strings.TrimSpace(s)
	if len(s) < 14 {
		return time.Time{}
	}
	base := s[:14]
	t, err := time.Parse("20060102150405", base)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func xmlEscapeAttr(s string) string { return xmlEscape(s) }

func hasSOAPFault(data []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Fault" {
			return true
		}
	}
}

func firstLocalText(data []byte, local string) string {
	vals := allLocalText(data, local)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func allLocalText(data []byte, local string) []string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != local {
			continue
		}
		var v string
		if err := dec.DecodeElement(&v, &se); err != nil {
			continue
		}
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

func eachNamedElement(data []byte, localName string) []map[string]string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []map[string]string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != localName {
			continue
		}
		fields := map[string]string{}
		depth := 1
		for depth > 0 {
			t, err := dec.Token()
			if err != nil {
				break
			}
			switch el := t.(type) {
			case xml.StartElement:
				depth++
				if depth == 2 {
					var v string
					name := el.Name.Local
					if err := dec.DecodeElement(&v, &el); err == nil {
						fields[name] = strings.TrimSpace(v)
					}
					depth--
				}
			case xml.EndElement:
				depth--
			}
		}
		out = append(out, fields)
	}
	return out
}
