package inbounds

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

const tcpTable = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0000000000000000 100 0 0 10 0
   1: 0100007F:20FB 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1 0000000000000000 100 0 0 10 0
   2: 0100007F:2383 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3 1 0000000000000000 100 0 0 10 0
   3: 0100007F:C350 0100007F:2383 01 00000000:00000000 00:00000000 00000000     0        0 4 1 0000000000000000 100 0 0 10 0
`
const udp6Table = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 5 2 0000000000000000 0
  1: 00000000000000000000000001000000:1F90 00000000000000000000000000000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 6 2 0000000000000000 0
`

func TestBoundAndPortInUse(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tcp"), []byte(tcpTable), 0o644)
	os.WriteFile(filepath.Join(dir, "udp6"), []byte(udp6Table), 0o644)
	old := ProcNet
	ProcNet = dir
	defer func() { ProcNet = old }()

	socks, ok := Bound()
	if !ok || len(socks) != 5 {
		t.Fatalf("bound: %v %+v", ok, socks)
	}
	want := map[string]bool{"tcp 0.0.0.0:443": true, "tcp 127.0.0.1:8443": true, "tcp 127.0.0.1:9091": true,
		"udp [::]:443": true, "udp [::1]:8080": true}
	for _, s := range socks {
		k := s.Network + " " + addrPort(s)
		if !want[k] {
			t.Errorf("unexpected socket %s", k)
		}
	}

	raw := []byte(cfg) // hy2 :: 443/udp, reality 127.0.0.1:8443/tcp, mixed 127.0.0.1:1080
	cases := []struct {
		f    Form
		want string
	}{
		{Form{Tag: "v", Type: "vless", Listen: "::", ListenPort: 443}, "tcp 0.0.0.0:443"}, // HAProxy holds TCP 443
		{Form{Tag: "hy2-in", Type: "hysteria2", Listen: "::", ListenPort: 443}, ""},       // sing-box's own hy2 socket
		{Form{Tag: "reality-in", Type: "vless", Listen: "127.0.0.1", ListenPort: 9091}, "tcp 127.0.0.1:9091"},
		{Form{Tag: "v", Type: "vless", Listen: "10.0.0.5", ListenPort: 9091}, ""},      // other address
		{Form{Tag: "h", Type: "hysteria2", Listen: "::", ListenPort: 9091}, ""},        // TCP only on 9091
		{Form{Tag: "h", Type: "hysteria2", Listen: "127.0.0.1", ListenPort: 8080}, ""}, // ::1 is not 127.0.0.1
		{Form{Tag: "h", Type: "hysteria2", Listen: "::", ListenPort: 8080}, "udp [::1]:8080"},
		{Form{Tag: "v", Type: "vless", Listen: "127.0.0.1", ListenPort: 50000}, ""}, // only an outgoing connection
	}
	for _, c := range cases {
		if got := PortInUse(raw, c.f, socks); got != c.want {
			t.Errorf("%s %s:%d = %q, want %q", c.f.Type, c.f.Listen, c.f.ListenPort, got, c.want)
		}
	}
}

func addrPort(s Socket) string { return netip.AddrPortFrom(s.Addr, uint16(s.Port)).String() }
