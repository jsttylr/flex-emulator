package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDiscoveryPacket(t *testing.T) {
	rc := defaultRadio()
	rc.Nickname = "My 7300"
	payload := discoveryPayload(rc, "192.168.1.20")
	pkt := buildDiscoveryPacket(5, payload)
	if len(pkt)%4 != 0 {
		t.Fatalf("packet not word aligned: %d", len(pkt))
	}
	h := binary.BigEndian.Uint32(pkt[0:])
	if h>>28 != 3 || h&(1<<27) == 0 {
		t.Fatalf("bad header %08x", h)
	}
	if int(h&0xFFFF)*4 != len(pkt) {
		t.Fatalf("size field %d words, packet %d bytes", h&0xFFFF, len(pkt))
	}
	if (h>>16)&0xF != 5 {
		t.Fatalf("packet count %d", (h>>16)&0xF)
	}
	if binary.BigEndian.Uint32(pkt[4:]) != 0x800 || binary.BigEndian.Uint32(pkt[8:]) != 0x1C2D || binary.BigEndian.Uint32(pkt[12:]) != 0x534CFFFF {
		t.Fatalf("bad stream/class id % x", pkt[4:16])
	}
	body := strings.TrimRight(string(pkt[28:]), "\x00")
	for _, want := range []string{"model=FLEX-6600", "nickname=My_7300", "ip=192.168.1.20", "port=4992", "status=Available"} {
		if !strings.Contains(body, want) {
			t.Errorf("payload missing %q: %s", want, body)
		}
	}
}

// fakeAG connects like an accessory and collects status lines.
type fakeAG struct {
	conn  net.Conn
	lines chan string
}

func dialAG(t *testing.T, port int) *fakeAG {
	t.Helper()
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	ag := &fakeAG{conn: c, lines: make(chan string, 200)}
	go func() {
		sc := bufio.NewScanner(c)
		for sc.Scan() {
			ag.lines <- sc.Text()
		}
		close(ag.lines)
	}()
	return ag
}

func (a *fakeAG) send(s string) { a.conn.Write([]byte(s + "\n")) }

// expect waits for a line containing all parts.
func (a *fakeAG) expect(t *testing.T, parts ...string) string {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case l, ok := <-a.lines:
			if !ok {
				t.Fatalf("connection closed waiting for %v", parts)
			}
			match := true
			for _, p := range parts {
				if !strings.Contains(l, p) {
					match = false
				}
			}
			if match {
				return l
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %v", parts)
		}
	}
}

func startTestRadio(t *testing.T, src SourceConfig, port int) *Radio {
	t.Helper()
	rc := defaultRadio()
	rc.APIPort = port
	rc.Source = src
	r := NewRadio(0, rc, "127.0.0.1")
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	return r
}

func TestAPIHandshakeAndManualUpdates(t *testing.T) {
	r := startTestRadio(t, SourceConfig{Type: "manual"}, 14992)
	ag := dialAG(t, 14992)
	ag.expect(t, "V1.")
	ag.expect(t, "H")
	ag.send("C1|client program AntennaGenius")
	ag.expect(t, "R1|0|")
	ag.send("C2|sub slice all")
	ag.expect(t, "R2|0|")
	ag.expect(t, "|slice 0", "RF_frequency=14.074000", "tx=1")
	ag.send("C3|sub radio all")
	ag.expect(t, "R3|0|")
	ag.expect(t, "interlock", "state=READY")

	r.update(func(s *RigState) { s.FreqHz = 7074000; s.Mode = "DIGU" })
	ag.expect(t, "slice 0", "RF_frequency=7.074000", "mode=DIGU")

	r.update(func(s *RigState) { s.PTT = true })
	ag.expect(t, "interlock", "state=TRANSMITTING")
	r.update(func(s *RigState) { s.PTT = false })
	ag.expect(t, "interlock", "state=READY")

	st := r.Status(0)
	if len(st.Clients) != 1 || !strings.Contains(st.Clients[0], "AntennaGenius") {
		t.Fatalf("clients = %v", st.Clients)
	}
}

func TestRigctldSource(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		sc := bufio.NewScanner(c)
		for sc.Scan() {
			switch sc.Text() {
			case "f":
				c.Write([]byte("21074000\n"))
			case "m":
				c.Write([]byte("PKTUSB\n3000\n"))
			case "t":
				c.Write([]byte("1\n"))
			}
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	r := startTestRadio(t, SourceConfig{Type: "rigctld", Host: "127.0.0.1", Port: port, PollMs: 50}, 14993)
	waitState(t, r, func(s RigState) bool { return s.FreqHz == 21074000 && s.Mode == "DIGU" && s.PTT && s.Connected })
}

func TestKenwoodTCPSource(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		rd := bufio.NewReader(c)
		for {
			cmd, err := rd.ReadString(';')
			if err != nil {
				return
			}
			if cmd == "IF;" {
				// TS-2000 IF answer: 18.100 MHz, transmitting, LSB
				c.Write([]byte("IF00018100000     +00000000011000000 ;"))
			}
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	r := startTestRadio(t, SourceConfig{Type: "kenwood-tcp", Host: "127.0.0.1", Port: port, PollMs: 50}, 14994)
	waitState(t, r, func(s RigState) bool { return s.FreqHz == 18100000 && s.Mode == "LSB" && s.PTT })
}

func TestTCISource(t *testing.T) {
	up := websocket.Upgrader{}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c, err := up.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		c.WriteMessage(websocket.TextMessage, []byte("protocol:ExpertSDR3,1.9;vfo:0,0,3573000;modulation:0,lsb;"))
		c.WriteMessage(websocket.TextMessage, []byte("trx:0,true;"))
		time.Sleep(2 * time.Second)
	})}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())
	port := ln.Addr().(*net.TCPAddr).Port
	r := startTestRadio(t, SourceConfig{Type: "tci", Host: "127.0.0.1", Port: port}, 14995)
	waitState(t, r, func(s RigState) bool { return s.FreqHz == 3573000 && s.Mode == "LSB" && s.PTT })
}

func TestCIVDecode(t *testing.T) {
	var got RigState
	upd := func(f func(*RigState)) { f(&got) }
	// IC-7300 (0x94) answering a frequency read: 14.074.000 Hz
	if !handleCIV([]byte{0xE0, 0x94, 0x03, 0x00, 0x40, 0x07, 0x14, 0x00}, 0x94, upd) || got.FreqHz != 14074000 {
		t.Fatalf("freq = %d", got.FreqHz)
	}
	handleCIV([]byte{0xE0, 0x94, 0x04, 0x03, 0x01}, 0x94, upd)
	handleCIV([]byte{0xE0, 0x94, 0x1C, 0x00, 0x01}, 0x94, upd)
	if got.Mode != "CW" || !got.PTT {
		t.Fatalf("state = %+v", got)
	}
	// Our own echoed query must be ignored.
	if handleCIV([]byte{0x94, 0xE0, 0x03}, 0x94, upd) {
		t.Fatal("echo was treated as a reply")
	}
	rd := bufio.NewReader(strings.NewReader("\x00\xFE\xFE\xE0\x94\x03\x00\x00\x10\x07\x00\xFD"))
	f, err := readCIVFrame(rd)
	if err != nil || bcdLE(f[3:8]) != 7100000 {
		t.Fatalf("frame %x err %v", f, err)
	}
}

func waitState(t *testing.T, r *Radio, ok func(RigState) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok(r.State()) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("state never matched: %+v", r.State())
}
