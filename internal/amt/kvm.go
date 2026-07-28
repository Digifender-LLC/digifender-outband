package amt

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	uriIPSKVMSetting     = "http://intel.com/wbem/wscim/1/ips-schema/1/IPS_KVMRedirectionSettingData"
	uriCIMKVMSAP         = "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_KVMRedirectionSAP"
	uriAMTRedirectionSvc = "http://intel.com/wbem/wscim/1/amt-schema/1/AMT_RedirectionService"
	uriIPSOptInService   = "http://intel.com/wbem/wscim/1/ips-schema/1/IPS_OptInService"
	actionPut            = "http://schemas.xmlsoap.org/ws/2004/09/transfer/Put"
)

// KVMStatus is a snapshot of AMT Hardware-KVM related WS-MAN state.
type KVMStatus struct {
	ListenerEnabled bool
	EnabledState    int // AMT_RedirectionService.EnabledState
	KVMEnabledState int // CIM_KVMRedirectionSAP.EnabledState (0 if class missing)
	KVMRequested    int
	HasKVMSAP       bool
	OptInRequired   int // IPS_OptInService.OptInRequired (0=none, 1=KVM, …)
	OptInState      int
	SettingFields   map[string]string // IPS_KVMRedirectionSettingData fields
	RawRedir        string
	RawKVMSAP       string
	RawSetting      string
}

// ProbeEnumerate is a recon helper that returns raw Pull XML for a resource URI.
func (a *Adapter) ProbeEnumerate(ctx context.Context, resourceURI string) ([]byte, error) {
	return a.ws.enumeratePull(ctx, resourceURI)
}

// KVMStatus queries redirection / KVM SAP / setting data.
func (a *Adapter) KVMStatus(ctx context.Context) (*KVMStatus, error) {
	st := &KVMStatus{SettingFields: map[string]string{}}

	redir, err := a.ws.enumeratePull(ctx, uriAMTRedirectionSvc)
	if err != nil {
		return nil, fmt.Errorf("AMT_RedirectionService: %w", err)
	}
	st.RawRedir = string(redir)
	items := eachNamedElement(redir, "AMT_RedirectionService")
	if len(items) > 0 {
		st.ListenerEnabled = strings.EqualFold(items[0]["ListenerEnabled"], "true")
		st.EnabledState, _ = strconv.Atoi(items[0]["EnabledState"])
	}

	sap, err := a.ws.enumeratePull(ctx, uriCIMKVMSAP)
	if err == nil {
		st.RawKVMSAP = string(sap)
		saps := eachNamedElement(sap, "CIM_KVMRedirectionSAP")
		if len(saps) > 0 {
			st.HasKVMSAP = true
			st.KVMEnabledState, _ = strconv.Atoi(saps[0]["EnabledState"])
			st.KVMRequested, _ = strconv.Atoi(saps[0]["RequestedState"])
		}
	} else if !isUnsupportedClass(err) {
		return st, fmt.Errorf("CIM_KVMRedirectionSAP: %w", err)
	}

	set, err := a.ws.enumeratePull(ctx, uriIPSKVMSetting)
	if err == nil {
		st.RawSetting = string(set)
		sets := eachNamedElement(set, "IPS_KVMRedirectionSettingData")
		if len(sets) > 0 {
			st.SettingFields = sets[0]
		}
	} else if !isUnsupportedClass(err) {
		return st, fmt.Errorf("IPS_KVMRedirectionSettingData: %w", err)
	}

	opt, err := a.ws.enumeratePull(ctx, uriIPSOptInService)
	if err == nil {
		opts := eachNamedElement(opt, "IPS_OptInService")
		if len(opts) > 0 {
			st.OptInRequired, _ = strconv.Atoi(opts[0]["OptInRequired"])
			st.OptInState, _ = strconv.Atoi(opts[0]["OptInState"])
		}
	}

	return st, nil
}

// kvmSAPEnabled reports whether the KVM SAP is in an enabled state (2 or 6).
func kvmSAPEnabled(enabledState int) bool {
	return enabledState == 2 || enabledState == 6
}

// EnableKVM turns on the redirection listener and enables CIM_KVMRedirectionSAP
// (RequestedState=2). Clears KVM opt-in when the ME allows it (MeshCommander-style)
// so a LAN management session can connect without local consent UI.
func (a *Adapter) EnableKVM(ctx context.Context) error {
	redir, err := a.ws.enumeratePull(ctx, uriAMTRedirectionSvc)
	if err != nil {
		return fmt.Errorf("enumerate AMT_RedirectionService: %w", err)
	}
	items := eachNamedElement(redir, "AMT_RedirectionService")
	if len(items) == 0 {
		return fmt.Errorf("AMT_RedirectionService: empty")
	}
	fields := items[0]
	enabledState, _ := strconv.Atoi(fields["EnabledState"])
	bits := enabledState & 3
	newState := 32768 + bits

	redirSel := keySelectors(fields, "Name", "CreationClassName", "SystemName", "SystemCreationClassName")
	_ = a.ws.requestStateChange(ctx, uriAMTRedirectionSvc, "AMT_RedirectionService", newState, redirSel)

	if err := a.clearKVMOptIn(ctx); err != nil {
		return err
	}

	sap, err := a.ws.enumeratePull(ctx, uriCIMKVMSAP)
	if err != nil {
		if isUnsupportedClass(err) {
			return fmt.Errorf("this AMT SKU has no CIM_KVMRedirectionSAP (no Hardware-KVM): %w", err)
		}
		return fmt.Errorf("enumerate CIM_KVMRedirectionSAP: %w", err)
	}
	saps := eachNamedElement(sap, "CIM_KVMRedirectionSAP")
	if len(saps) == 0 {
		return fmt.Errorf("CIM_KVMRedirectionSAP: empty (no Hardware-KVM)")
	}
	sapFields := saps[0]
	sapSel := keySelectors(sapFields, "Name", "CreationClassName", "SystemName", "SystemCreationClassName")
	cur, _ := strconv.Atoi(sapFields["EnabledState"])
	if !kvmSAPEnabled(cur) {
		if err := a.ws.requestStateChange(ctx, uriCIMKVMSAP, "CIM_KVMRedirectionSAP", 2, sapSel); err != nil {
			return fmt.Errorf("CIM_KVMRedirectionSAP RequestStateChange(2): %w", err)
		}
	}

	fields["ListenerEnabled"] = "true"
	fields["EnabledState"] = strconv.Itoa(newState)
	if err := a.ws.putInstance(ctx, uriAMTRedirectionSvc, "AMT_RedirectionService", fields, redirSel); err != nil {
		return fmt.Errorf("PUT AMT_RedirectionService: %w", err)
	}
	return nil
}

// clearKVMOptIn disables user-consent for KVM when the ME permits policy changes.
func (a *Adapter) clearKVMOptIn(ctx context.Context) error {
	set, err := a.ws.enumeratePull(ctx, uriIPSKVMSetting)
	if err != nil {
		if isUnsupportedClass(err) {
			return nil
		}
		return fmt.Errorf("enumerate IPS_KVMRedirectionSettingData: %w", err)
	}
	sets := eachNamedElement(set, "IPS_KVMRedirectionSettingData")
	if len(sets) > 0 && strings.EqualFold(sets[0]["OptInPolicy"], "true") {
		fields := sets[0]
		fields["OptInPolicy"] = "false"
		sel := map[string]string{"InstanceID": fields["InstanceID"]}
		if err := a.ws.putInstance(ctx, uriIPSKVMSetting, "IPS_KVMRedirectionSettingData", fields, sel); err != nil {
			return fmt.Errorf("PUT IPS_KVMRedirectionSettingData OptInPolicy=false: %w", err)
		}
	}

	opt, err := a.ws.enumeratePull(ctx, uriIPSOptInService)
	if err != nil {
		if isUnsupportedClass(err) {
			return nil
		}
		return fmt.Errorf("enumerate IPS_OptInService: %w", err)
	}
	opts := eachNamedElement(opt, "IPS_OptInService")
	if len(opts) == 0 {
		return nil
	}
	o := opts[0]
	req, _ := strconv.Atoi(o["OptInRequired"])
	canMod, _ := strconv.Atoi(o["CanModifyOptInPolicy"])
	if req == 0 || canMod == 0 {
		return nil
	}
	o["OptInRequired"] = "0"
	sel := keySelectors(o, "Name", "CreationClassName", "SystemName", "SystemCreationClassName")
	if err := a.ws.putInstance(ctx, uriIPSOptInService, "IPS_OptInService", o, sel); err != nil {
		return fmt.Errorf("PUT IPS_OptInService OptInRequired=0: %w", err)
	}
	return nil
}

func keySelectors(fields map[string]string, keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v := fields[k]; v != "" {
			out[k] = v
		}
	}
	return out
}

func (c *wsmanClient) requestStateChange(ctx context.Context, resourceURI, className string, requestedState int, selectors map[string]string) error {
	action := resourceURI + "/RequestStateChange"
	var sel strings.Builder
	sel.WriteString(`<w:SelectorSet>`)
	for k, v := range selectors {
		sel.WriteString(`<w:Selector Name="`)
		sel.WriteString(xmlEscape(k))
		sel.WriteString(`">`)
		sel.WriteString(xmlEscape(v))
		sel.WriteString(`</w:Selector>`)
	}
	sel.WriteString(`</w:SelectorSet>`)
	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns="http://www.w3.org/2003/05/soap-envelope">`+
		`<Header>`+
		`<a:Action>%s</a:Action><a:To>/wsman</a:To>`+
		`<w:ResourceURI>%s</w:ResourceURI>`+
		`<a:MessageID>%s</a:MessageID>`+
		`<a:ReplyTo><a:Address>%s</a:Address></a:ReplyTo>`+
		`%s`+
		`</Header>`+
		`<Body>`+
		`<r:RequestStateChange_INPUT xmlns:r="%s">`+
		`<r:RequestedState>%d</r:RequestedState>`+
		`</r:RequestStateChange_INPUT>`+
		`</Body></Envelope>`,
		action, resourceURI, c.nextMsgID(), addrAnonymous, sel.String(), resourceURI, requestedState)
	data, err := c.post(ctx, body)
	if err != nil {
		return err
	}
	if rc := firstLocalText(data, "ReturnValue"); rc != "" && rc != "0" {
		return fmt.Errorf("%s RequestStateChange ReturnValue=%s", className, rc)
	}
	return nil
}

func (c *wsmanClient) putInstance(ctx context.Context, resourceURI, className string, fields map[string]string, selectors map[string]string) error {
	var sel strings.Builder
	sel.WriteString(`<w:SelectorSet>`)
	for k, v := range selectors {
		sel.WriteString(`<w:Selector Name="`)
		sel.WriteString(xmlEscape(k))
		sel.WriteString(`">`)
		sel.WriteString(xmlEscape(v))
		sel.WriteString(`</w:Selector>`)
	}
	sel.WriteString(`</w:SelectorSet>`)
	var props strings.Builder
	for k, v := range fields {
		if strings.HasPrefix(k, "__") || k == "" {
			continue
		}
		props.WriteString(`<r:`)
		props.WriteString(k)
		props.WriteString(`>`)
		props.WriteString(xmlEscape(v))
		props.WriteString(`</r:`)
		props.WriteString(k)
		props.WriteString(`>`)
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<Envelope xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns="http://www.w3.org/2003/05/soap-envelope">`+
		`<Header>`+
		`<a:Action>%s</a:Action><a:To>/wsman</a:To>`+
		`<w:ResourceURI>%s</w:ResourceURI>`+
		`<a:MessageID>%s</a:MessageID>`+
		`<a:ReplyTo><a:Address>%s</a:Address></a:ReplyTo>`+
		`<w:OperationTimeout>PT60.000S</w:OperationTimeout>`+
		`%s`+
		`</Header>`+
		`<Body>`+
		`<r:%s xmlns:r="%s">%s</r:%s>`+
		`</Body></Envelope>`,
		actionPut, resourceURI, c.nextMsgID(), addrAnonymous, sel.String(),
		className, resourceURI, props.String(), className)
	_, err := c.post(ctx, body)
	return err
}
