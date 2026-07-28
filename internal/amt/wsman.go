package amt

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	uriCIMPowerAssoc   = "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_AssociatedPowerManagementService"
	uriCIMPowerSvc     = "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_PowerManagementService"
	uriCIMComputerSys  = "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_ComputerSystem"
	uriCIMSoftwareID   = "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_SoftwareIdentity"
	uriCIMNumericSens  = "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_NumericSensor"
	uriAMTEventLog    = "http://intel.com/wbem/wscim/1/amt-schema/1/AMT_EventLogEntry"
	actionEnumerate   = "http://schemas.xmlsoap.org/ws/2004/09/enumeration/Enumerate"
	actionPull         = "http://schemas.xmlsoap.org/ws/2004/09/enumeration/Pull"
	actionGet          = "http://schemas.xmlsoap.org/ws/2004/09/transfer/Get"
	actionPowerChange  = "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_PowerManagementService/RequestPowerStateChange"
	addrAnonymous      = "http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous"
)

// wsmanClient talks HTTP Digest WS-MAN to an Intel AMT ME.
type wsmanClient struct {
	baseURL string
	http    *http.Client
	msgID   uint64
}

func newWSMAN(cfg Config) *wsmanClient {
	port := cfg.Port
	if port == 0 {
		if cfg.TLS {
			port = 16993
		} else {
			port = 16992
		}
	}
	scheme := "http"
	if cfg.TLS {
		scheme = "https"
	}
	transport := &digestTransport{
		user:     cfg.User,
		password: cfg.Password,
		base: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // AMT uses self-signed ME certs
			MaxIdleConns:        2,
			IdleConnTimeout:     60 * time.Second,
		},
	}
	return &wsmanClient{
		baseURL: fmt.Sprintf("%s://%s:%d/wsman", scheme, cfg.Host, port),
		http: &http.Client{
			Transport: transport,
			Timeout:   45 * time.Second,
		},
	}
}

func (c *wsmanClient) nextMsgID() string {
	c.msgID++
	return fmt.Sprintf("uuid:%d", c.msgID)
}

func (c *wsmanClient) post(ctx context.Context, body string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/soap+xml;charset=UTF-8")
	req.Header.Set("User-Agent", "outband-amt")
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
		snip := string(data)
		if len(snip) > 400 {
			snip = snip[:400]
		}
		return nil, fmt.Errorf("amt wsman HTTP %d: %s", resp.StatusCode, snip)
	}
	if hasSOAPFault(data) {
		if reason := firstLocalText(data, "Text"); reason != "" {
			return data, fmt.Errorf("amt wsman fault: %s", reason)
		}
		return data, fmt.Errorf("amt wsman SOAP fault")
	}
	return data, nil
}

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

func (c *wsmanClient) get(ctx context.Context, resourceURI string, selectors map[string]string) ([]byte, error) {
	var sel strings.Builder
	if len(selectors) > 0 {
		sel.WriteString(`<w:SelectorSet>`)
		for k, v := range selectors {
			sel.WriteString(`<w:Selector Name="`)
			sel.WriteString(xmlEscape(k))
			sel.WriteString(`">`)
			sel.WriteString(xmlEscape(v))
			sel.WriteString(`</w:Selector>`)
		}
		sel.WriteString(`</w:SelectorSet>`)
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns="http://www.w3.org/2003/05/soap-envelope">`+
		`<Header>`+
		`<a:Action>%s</a:Action><a:To>/wsman</a:To>`+
		`<w:ResourceURI>%s</w:ResourceURI>`+
		`<a:MessageID>%s</a:MessageID>`+
		`<a:ReplyTo><a:Address>%s</a:Address></a:ReplyTo>`+
		`%s`+
		`</Header><Body/></Envelope>`,
		actionGet, resourceURI, c.nextMsgID(), addrAnonymous, sel.String())
	return c.post(ctx, body)
}

func (c *wsmanClient) enumeratePull(ctx context.Context, resourceURI string) ([]byte, error) {
	enumBody := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns="http://www.w3.org/2003/05/soap-envelope">`+
		`<Header>`+
		`<a:Action>%s</a:Action><a:To>/wsman</a:To>`+
		`<w:ResourceURI>%s</w:ResourceURI>`+
		`<a:MessageID>%s</a:MessageID>`+
		`<a:ReplyTo><a:Address>%s</a:Address></a:ReplyTo>`+
		`</Header>`+
		`<Body><Enumerate xmlns="http://schemas.xmlsoap.org/ws/2004/09/enumeration"/></Body></Envelope>`,
		actionEnumerate, resourceURI, c.nextMsgID(), addrAnonymous)
	enumResp, err := c.post(ctx, enumBody)
	if err != nil {
		return nil, err
	}
	ec := firstLocalText(enumResp, "EnumerationContext")
	if ec == "" {
		return nil, fmt.Errorf("amt: missing EnumerationContext")
	}
	pullBody := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns="http://www.w3.org/2003/05/soap-envelope">`+
		`<Header>`+
		`<a:Action>%s</a:Action><a:To>/wsman</a:To>`+
		`<w:ResourceURI>%s</w:ResourceURI>`+
		`<a:MessageID>%s</a:MessageID>`+
		`<a:ReplyTo><a:Address>%s</a:Address></a:ReplyTo>`+
		`</Header>`+
		`<Body><Pull xmlns="http://schemas.xmlsoap.org/ws/2004/09/enumeration">`+
		`<EnumerationContext>%s</EnumerationContext><MaxElements>999</MaxElements>`+
		`</Pull></Body></Envelope>`,
		actionPull, resourceURI, c.nextMsgID(), addrAnonymous, xmlEscape(ec))
	return c.post(ctx, pullBody)
}

func (c *wsmanClient) requestPowerState(ctx context.Context, powerState int) error {
	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns="http://www.w3.org/2003/05/soap-envelope">`+
		`<Header>`+
		`<a:Action>%s</a:Action><a:To>/wsman</a:To>`+
		`<w:ResourceURI>%s</w:ResourceURI>`+
		`<a:MessageID>%s</a:MessageID>`+
		`<a:ReplyTo><a:Address>%s</a:Address></a:ReplyTo>`+
		`<w:SelectorSet>`+
		`<w:Selector Name="CreationClassName">CIM_PowerManagementService</w:Selector>`+
		`<w:Selector Name="Name">Intel(r) AMT Power Management Service</w:Selector>`+
		`<w:Selector Name="SystemCreationClassName">CIM_ComputerSystem</w:Selector>`+
		`<w:Selector Name="SystemName">Intel(r) AMT</w:Selector>`+
		`</w:SelectorSet>`+
		`</Header>`+
		`<Body>`+
		`<r:RequestPowerStateChange_INPUT xmlns:r="%s">`+
		`<r:PowerState>%d</r:PowerState>`+
		`<r:ManagedElement>`+
		`<a:Address>%s</a:Address>`+
		`<a:ReferenceParameters>`+
		`<w:ResourceURI>%s</w:ResourceURI>`+
		`<w:SelectorSet>`+
		`<w:Selector Name="CreationClassName">CIM_ComputerSystem</w:Selector>`+
		`<w:Selector Name="Name">ManagedSystem</w:Selector>`+
		`</w:SelectorSet>`+
		`</a:ReferenceParameters>`+
		`</r:ManagedElement>`+
		`</r:RequestPowerStateChange_INPUT>`+
		`</Body></Envelope>`,
		actionPowerChange, uriCIMPowerSvc, c.nextMsgID(), addrAnonymous,
		uriCIMPowerSvc, powerState, addrAnonymous, uriCIMComputerSys)
	data, err := c.post(ctx, body)
	if err != nil {
		return err
	}
	if rc := firstLocalText(data, "ReturnValue"); rc != "" && rc != "0" {
		return fmt.Errorf("amt RequestPowerStateChange ReturnValue=%s", rc)
	}
	return nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// firstLocalText returns the text of the first element whose local name matches.
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

// eachElement walks top-level instances of localName under Items (or whole doc) and
// collects child local-name → text maps.
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
