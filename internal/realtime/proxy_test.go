package realtime

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A CONNECT proxy must hijack the connection before tunneling WebSocket data.
// Returning a normal 2xx HTTP response no longer keeps the connection alive on
// current Go versions; the dependency's older proxy fixtures rely on that.
func TestWebSocketCONNECTProxy(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		t.Run(fmt.Sprintf("authenticated=%t", authenticated), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer conn.Close()
				conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				kind, data, err := conn.ReadMessage()
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				if err := conn.WriteMessage(kind, data); err != nil {
					t.Errorf("echo: %v", err)
				}
			}))
			defer upstream.Close()
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodConnect || r.Host != upstream.Listener.Addr().String() {
					t.Errorf("unexpected proxy request: %s %s", r.Method, r.Host)
					http.Error(w, "invalid CONNECT", http.StatusBadRequest)
					return
				}
				wantAuth := ""
				if authenticated {
					wantAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte("user:password"))
				}
				if r.Header.Get("Proxy-Authorization") != wantAuth {
					t.Error("incorrect proxy authorization")
					http.Error(w, "authentication required", http.StatusProxyAuthRequired)
					return
				}
				backend, err := net.DialTimeout("tcp", upstream.Listener.Addr().String(), time.Second)
				if err != nil {
					t.Errorf("connect backend: %v", err)
					return
				}
				defer backend.Close()
				client, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("hijack: %v", err)
					return
				}
				defer client.Close()
				client.SetDeadline(time.Now().Add(5 * time.Second))
				backend.SetDeadline(time.Now().Add(5 * time.Second))
				if _, err := fmt.Fprint(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
					t.Errorf("CONNECT response: %v", err)
					return
				}
				if err := buffered.Flush(); err != nil {
					t.Errorf("flush: %v", err)
					return
				}
				done := make(chan struct{})
				go func() {
					io.Copy(backend, buffered)
					backend.Close()
					close(done)
				}()
				io.Copy(client, backend)
				client.Close()
				<-done
			}))
			defer proxy.Close()
			proxyURL, err := url.Parse(proxy.URL)
			if err != nil {
				t.Fatal(err)
			}
			if authenticated {
				proxyURL.User = url.UserPassword("user", "password")
			}
			dialer := websocket.Dialer{Proxy: http.ProxyURL(proxyURL), HandshakeTimeout: 5 * time.Second}
			conn, _, err := dialer.Dial("ws"+upstream.URL[len("http"):], nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			const payload = "audio through CONNECT"
			if err := conn.WriteMessage(websocket.BinaryMessage, []byte(payload)); err != nil {
				t.Fatal(err)
			}
			kind, data, err := conn.ReadMessage()
			if err != nil || kind != websocket.BinaryMessage || string(data) != payload {
				t.Fatalf("echo: kind=%d data=%q err=%v", kind, data, err)
			}
		})
	}
}
