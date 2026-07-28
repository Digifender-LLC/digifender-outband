//go:build ignore
package main

import (
	"context"
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
	pass := os.Getenv("OUTBAND_BMC_PASS")
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	hc := &http.Client{Timeout: 20 * time.Second, Transport: tr}

	form := url.Values{"WEBVAR_USERNAME": {"root"}, "WEBVAR_PASSWORD": {pass}}
	// try both http and https login
	for _, scheme := range []string{"https", "http"} {
		resp, err := hc.PostForm(scheme+"://"+host+"/rpc/WEBSES/create.asp", form)
		if err != nil {
			fmt.Println(scheme, "login", err)
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		m := regexp.MustCompile(`'SESSION_COOKIE'\s*:\s*'([^']*)'`).FindStringSubmatch(string(b))
		if m == nil {
			fmt.Println(scheme, "login no cookie", string(b)[:min(120, len(b))])
			continue
		}
		cookie := m[1]
		fmt.Println(scheme, "login cookie", cookie)

		for _, jnlpScheme := range []string{"https", "http"} {
			u := jnlpScheme + "://" + host + "/Java/jviewer.jnlp?EXTRNIP=" + url.QueryEscape(host) + "&JNLPSTR=JViewer"
			req, _ := http.NewRequest("GET", u, nil)
			req.Header.Set("Cookie", "SessionCookie="+cookie)
			resp, err = hc.Do(req)
			if err != nil {
				fmt.Println(" ", jnlpScheme, "jnlp", err)
				continue
			}
			jb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			body := strings.ReplaceAll(string(jb), "\x02<argument>", "</argument><argument>")
			args := regexp.MustCompile(`(?s)<argument>(.*?)</argument>`).FindAllStringSubmatch(body, -1)
			var raw []string
			for _, a := range args {
				v := strings.TrimSpace(a[1])
				if v != "" {
					raw = append(raw, v)
				}
			}
			if len(raw) < 3 {
				fmt.Printf("  %s jnlp bad args %v\n", jnlpScheme, raw)
				continue
			}
			token := raw[2]
			fmt.Printf("  %s jnlp token=%q\n", jnlpScheme, token)

			sum := md5.Sum([]byte(token))
			conn, err := net.DialTimeout("tcp", host+":7578", 5*time.Second)
			if err != nil {
				fmt.Println("  dial", err)
				continue
			}
			pkt := make([]byte, 23)
			pkt[0] = 34
			binary.LittleEndian.PutUint32(pkt[1:5], 16)
			copy(pkt[7:], sum[:])
			_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
			_, _ = conn.Write(pkt)
			buf := make([]byte, 8)
			n, err := io.ReadFull(conn, buf)
			conn.Close()
			body0 := byte(255)
			if n >= 8 {
				body0 = 0
				// need size
				sz := binary.LittleEndian.Uint32(buf[1:5])
				if sz >= 1 {
					// already consumed? we only read 8 = hdr+1 body byte
					body0 = buf[7]
				}
			}
			fmt.Printf("  validate body0=%d n=%d err=%v hex=%x\n", body0, n, err, buf[:n])
			if body0 != 0 && body0 != 255 {
				fmt.Println("SUCCESS", scheme, jnlpScheme)
				_ = context.Background()
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		req, _ := http.NewRequest("GET", scheme+"://"+host+"/rpc/WEBSES/logout.asp", nil)
		req.Header.Set("Cookie", "SessionCookie="+cookie)
		hc.Do(req)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
