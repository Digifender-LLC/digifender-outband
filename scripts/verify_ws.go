//go:build ignore

package main

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.PostForm("http://127.0.0.1:8080/login", url.Values{"password": {"devpass"}})
	if err != nil {
		fmt.Println("login err", err)
		os.Exit(1)
	}
	resp.Body.Close()

	dialer := websocket.Dialer{Jar: jar, HandshakeTimeout: 20 * time.Second}
	hostID := os.Getenv("OUTBAND_DEFAULT_HOST")
	if hostID == "" {
		hostID = "tyan"
	}
	conn, _, err := dialer.Dial("ws://127.0.0.1:8080/h/"+hostID+"/ws/sol", nil)
	if err != nil {
		fmt.Println("ws dial FAILED:", err)
		os.Exit(1)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	mt, data, err := conn.ReadMessage()
	if err != nil {
		fmt.Println("ws read FAILED:", err)
		os.Exit(1)
	}
	msg := strings.ReplaceAll(string(data), "\r", "\\r")
	msg = strings.ReplaceAll(msg, "\n", "\\n")
	if len(msg) > 160 {
		msg = msg[:160] + "…"
	}
	fmt.Printf("ws OK mt=%d msg=%q\n", mt, msg)
}
