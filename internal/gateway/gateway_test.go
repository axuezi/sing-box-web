package gateway

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestFeedAndClient(t *testing.T) {
	r := &Resolver{}
	r.Feed("<134>Oct 13 22:30:00 haproxy[8]: sbw-gw 203.0.113.9 127.0.0.1:41234")
	r.Feed("sbw-gw ::ffff:198.51.100.2 127.0.0.1:41235")
	r.Feed("sbw-gw 192.0.2.1 -:-")        // refused before reaching sing-box
	r.Feed("other 192.0.2.1 127.0.0.1:1") // not ours
	for _, c := range []struct {
		ip, port, want string
		ok             bool
	}{
		{"127.0.0.1", "41234", "203.0.113.9", true},
		{"::ffff:127.0.0.1", "41235", "198.51.100.2", true},
		{"127.0.0.1", "1", "", false},
		{"127.0.0.1", "9", "", false},
	} {
		got, ok := r.Client(c.ip, c.port)
		if got != c.want || ok != c.ok {
			t.Errorf("Client(%s, %s) = %q, %v; want %q, %v", c.ip, c.port, got, ok, c.want, c.ok)
		}
	}
	var nilR *Resolver
	if _, ok := nilR.Client("127.0.0.1", "41234"); ok {
		t.Error("nil resolver resolved")
	}
}

func TestExpiry(t *testing.T) {
	now := time.Now()
	r := &Resolver{Now: func() time.Time { return now }}
	r.Feed("sbw-gw 203.0.113.9 127.0.0.1:5000")
	now = now.Add(entryTTL + time.Minute)
	if _, ok := r.Client("127.0.0.1", "5000"); ok {
		t.Error("expired entry resolved")
	}
}

func TestRunUDP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	pc.Close()
	r := &Resolver{Listen: addr}
	go r.Run(ctx)
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn.Write([]byte("sbw-gw 203.0.113.7 127.0.0.1:6000"))
		if ip, ok := r.Client("127.0.0.1", "6000"); ok && ip == "203.0.113.7" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("datagram not received")
}
