package netutil

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseProxyURL(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantScheme string
		wantErr    bool
	}{
		{name: "socks5", raw: "socks5://warp:1080", wantScheme: "socks5"},
		{name: "socks5h normalized", raw: "socks5h://warp:1080", wantScheme: "socks5"},
		{name: "with credentials", raw: "socks5://user:pass@warp:1080", wantScheme: "socks5"},
		{name: "http rejected", raw: "http://proxy:8080", wantErr: true},
		{name: "https rejected", raw: "https://proxy:8080", wantErr: true},
		{name: "missing host", raw: "socks5://", wantErr: true},
		{name: "no scheme", raw: "warp:1080", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseProxyURL(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseProxyURL(%q) succeeded, want error", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseProxyURL(%q) returned error: %v", tt.raw, err)
			}
			if parsed.Scheme != tt.wantScheme {
				t.Fatalf("scheme = %q, want %q", parsed.Scheme, tt.wantScheme)
			}
		})
	}
}

func TestNewHTTPClientDirect(t *testing.T) {
	client, err := NewHTTPClient("", 5*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPClient returned error: %v", err)
	}
	if client.Transport != nil {
		t.Fatal("direct client must not carry a custom transport")
	}
}

func TestNewDialContextEmpty(t *testing.T) {
	dial, err := NewDialContext("")
	if err != nil {
		t.Fatalf("NewDialContext returned error: %v", err)
	}
	if dial != nil {
		t.Fatal("empty proxy url must yield a nil dial function")
	}
}

func TestNewHTTPClientThroughSocks5(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "tunneled")
	}))
	defer server.Close()

	proxyAddr, tunnels := startSocks5Server(t)

	client, err := NewHTTPClient("socks5://"+proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("NewHTTPClient returned error: %v", err)
	}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("GET through proxy failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "tunneled" {
		t.Fatalf("body = %q, want %q", body, "tunneled")
	}
	if tunnels.Load() == 0 {
		t.Fatal("request did not pass through the SOCKS5 proxy")
	}
}

func TestNewDialContextThroughSocks5(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn)
	}()

	proxyAddr, tunnels := startSocks5Server(t)

	dial, err := NewDialContext("socks5h://" + proxyAddr)
	if err != nil {
		t.Fatalf("NewDialContext returned error: %v", err)
	}
	if dial == nil {
		t.Fatal("expected a dial function for a configured proxy")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", echo.Addr().String())
	if err != nil {
		t.Fatalf("dial through proxy failed: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q, want %q", buf, "ping")
	}
	if tunnels.Load() == 0 {
		t.Fatal("connection did not pass through the SOCKS5 proxy")
	}
}

// startSocks5Server runs a minimal no-auth SOCKS5 CONNECT proxy and returns
// its address plus a counter of successfully tunneled connections.
func startSocks5Server(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	tunnels := &atomic.Int32{}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveSocks5Conn(conn, tunnels)
		}
	}()
	return listener.Addr().String(), tunnels
}

func serveSocks5Conn(conn net.Conn, tunnels *atomic.Int32) {
	defer conn.Close()

	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil || greeting[0] != 0x05 {
		return
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	conn.Write([]byte{0x05, 0x00})

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil || head[1] != 0x01 {
		return
	}
	var host string
	switch head[3] {
	case 0x01:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return
		}
		name := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return
		}
		host = string(name)
	default:
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBytes)

	target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()
	tunnels.Add(1)
	conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})

	go io.Copy(target, conn)
	io.Copy(conn, target)
}
