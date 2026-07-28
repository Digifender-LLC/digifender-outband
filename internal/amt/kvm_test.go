package amt

import (
	"strings"
	"testing"
)

func TestKVMURIs(t *testing.T) {
	if !strings.Contains(uriIPSKVMSetting, "IPS_KVMRedirectionSettingData") {
		t.Fatal(uriIPSKVMSetting)
	}
	if !strings.Contains(uriCIMKVMSAP, "CIM_KVMRedirectionSAP") {
		t.Fatal(uriCIMKVMSAP)
	}
	if !strings.Contains(uriAMTRedirectionSvc, "AMT_RedirectionService") {
		t.Fatal(uriAMTRedirectionSvc)
	}
}
