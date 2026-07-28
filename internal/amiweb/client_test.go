package amiweb_test

import (
	"testing"

	"mipmi/internal/amiweb"
)

func TestParseJNLPPositionalCorrupt(t *testing.T) {
	body := `<?xml version="1.0"?>
<jnlp>
  <application-desc>
    <argument>192.168.9.74</argument>
    <argument>7578</argument>
    <argument>ABC123token` + "\x02" + `<argument>SessionCookieValue000</argument>
  </application-desc>
</jnlp>`
	args := amiweb.ParseJNLPArgs(body, "fallback")
	if args["kvmtoken"] != "ABC123token" {
		t.Fatalf("kvmtoken=%q args=%v", args["kvmtoken"], args)
	}
	if args["webcookie"] != "SessionCookieValue000" {
		t.Fatalf("webcookie=%q", args["webcookie"])
	}
	if args["kvmport"] != "7578" {
		t.Fatalf("port=%q", args["kvmport"])
	}
}

func TestParseJNLPNamed(t *testing.T) {
	body := `
    <argument>-kvmtoken</argument><argument>TOK</argument>
    <argument>-webcookie</argument><argument>CK</argument>
    <argument>-kvmport</argument><argument>7582</argument>
`
	args := amiweb.ParseJNLPArgs(body, "")
	if args["kvmtoken"] != "TOK" || args["webcookie"] != "CK" || args["kvmport"] != "7582" {
		t.Fatalf("%v", args)
	}
}
