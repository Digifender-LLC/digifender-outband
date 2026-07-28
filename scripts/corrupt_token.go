//go:build ignore
package main

import (
	"crypto/md5"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

func main() {
	host := "192.168.9.74"
	pass := os.Getenv("MIPMI_BMC_PASS")
	hc := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	form := url.Values{"WEBVAR_USERNAME": {"root"}, "WEBVAR_PASSWORD": {pass}}
	resp, _ := hc.PostForm("http://"+host+"/rpc/WEBSES/create.asp", form)
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)); resp.Body.Close()
	cookie := regexp.MustCompile(`'SESSION_COOKIE'\s*:\s*'([^']*)'`).FindStringSubmatch(string(b))[1]

	req, _ := http.NewRequest("GET", "http://"+host+"/Java/jviewer.jnlp?EXTRNIP="+host+"&JNLPSTR=JViewer", nil)
	req.Header.Set("Cookie", "SessionCookie="+cookie)
	resp, _ = hc.Do(req)
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)); resp.Body.Close()

	// extract raw application-desc section bytes
	idx := strings.Index(string(raw), "<application-desc>")
	fmt.Printf("jnlp len=%d app-desc at %d\n", len(raw), idx)
	section := raw[idx:]
	fmt.Printf("section hex around args:\n")
	fmt.Printf("raw section:\n%s\n", string(section))

	// Parse the CORRUPT way Java might: split by <argument> and take text until < or end
	parts := strings.Split(string(raw), "<argument>")
	fmt.Println("split count", len(parts))
	for i, p := range parts {
		if i == 0 { continue }
		// take until </argument> OR end of meaningful
		end := strings.Index(p, "</argument>")
		var val string
		if end >= 0 {
			val = p[:end]
		} else {
			val = p // includes nested garbage
			if j := strings.Index(val, "</application"); j >= 0 {
				val = val[:j]
			}
		}
		fmt.Printf("arg%d repr=%q len=%d hex=%x\n", i-1, val, len(val), val)
	}

	// Also repaired parse
	body := strings.ReplaceAll(string(raw), "\x02<argument>", "</argument><argument>")
	body = strings.ReplaceAll(body, "\x02", "")
	ms := regexp.MustCompile(`(?s)<argument>(.*?)</argument>`).FindAllStringSubmatch(body, -1)
	var repaired []string
	for _, m := range ms {
		repaired = append(repaired, strings.TrimSpace(m[1]))
	}
	fmt.Println("repaired", repaired)

	token := repaired[2]
	web := repaired[3]

	// Build candidate secrets
	type cand struct{ n string; s string }
	cands := []cand{
		{"clean token", token},
		{"token+\\x02", token + "\x02"},
		{"token+\\x02<argument>", token + "\x02<argument>"},
		{"token+\\x02+web", token + "\x02" + web},
		{"token+web", token + web},
		{"web", web},
		{"cookie", cookie},
	}
	// from corrupt split
	if len(parts) > 3 {
		corrupt := parts[3]
		if end := strings.Index(corrupt, "</argument>"); end >= 0 {
			cands = append(cands, cand{"corrupt-to-close", corrupt[:end]})
		}
		cands = append(cands, cand{"corrupt-full-third", strings.Split(corrupt, "</application")[0]})
	}

	for _, c := range cands {
		sum := md5.Sum([]byte(c.s))
		conn, err := net.DialTimeout("tcp", host+":7578", 4*time.Second)
		if err != nil {
			fmt.Println(c.n, "dial", err)
			break
		}
		pkt := make([]byte, 23)
		pkt[0] = 34
		binary.LittleEndian.PutUint32(pkt[1:5], 16)
		copy(pkt[7:], sum[:])
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		conn.Write(pkt)
		buf := make([]byte, 8)
		n, err := io.ReadFull(conn, buf)
		conn.Close()
		body0 := byte(255)
		if n >= 8 {
			body0 = buf[7]
		}
		fmt.Printf("%s (len=%d): body0=%d err=%v\n", c.n, len(c.s), body0, err)
		if body0 != 0 && body0 != 255 {
			fmt.Println("SUCCESS with", c.n, "secret", c.s)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	req, _ = http.NewRequest("GET", "http://"+host+"/rpc/WEBSES/logout.asp", nil)
	req.Header.Set("Cookie", "SessionCookie="+cookie)
	hc.Do(req)
}
