package main

import (
	"bufio"
	"context"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// apiClient is one accessory (Antenna Genius, SmartSDR, ...) connected to the TCP API.
type apiClient struct {
	conn    net.Conn
	remote  string
	handle  uint32
	program atomic.Value // string, from "client program"

	wmu  sync.Mutex
	subs map[string]bool // keyed by object: slice, radio, tx, interlock, client
}

func (c *apiClient) send(line string) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	c.conn.Write([]byte(line + "\n"))
}

func (c *apiClient) subscribed(obj string) bool {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.subs[obj]
}

func (r *Radio) serveAPI(ctx context.Context, ln net.Listener) {
	logs.Add(r.tag, "api: listening on %s", ln.Addr())
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil {
				logs.Add(r.tag, "api: accept failed: %v", err)
			}
			return
		}
		go safely("api client", func() { r.handleClient(conn) })
	}
}

// broadcast sends a status object to every client subscribed to obj.
func (r *Radio) broadcast(obj, status string) {
	r.mu.Lock()
	targets := make([]*apiClient, 0, len(r.clients))
	for c := range r.clients {
		targets = append(targets, c)
	}
	r.mu.Unlock()
	for _, c := range targets {
		if c.subscribed(obj) {
			r.status(c, status)
		}
	}
}

func (r *Radio) status(c *apiClient, status string) {
	line := fmt.Sprintf("S%08X|%s", r.guiHandle, status)
	logs.Add(r.tag, "-> %s %s", c.remote, line)
	c.send(line)
}

func (r *Radio) handleClient(conn net.Conn) {
	c := &apiClient{
		conn:   conn,
		remote: conn.RemoteAddr().String(),
		handle: 0x20000000 | rand.Uint32()&0x0FFFFFFF,
		subs:   map[string]bool{},
	}
	r.mu.Lock()
	r.clients[c] = struct{}{}
	r.mu.Unlock()
	logs.Add(r.tag, "api: %s connected", c.remote)
	defer func() {
		r.mu.Lock()
		delete(r.clients, c)
		r.mu.Unlock()
		conn.Close()
		logs.Add(r.tag, "api: %s disconnected", c.remote)
	}()

	c.send("V1.4.0.0")
	c.send(fmt.Sprintf("H%08X", c.handle))
	// Real radios announce existing GUI clients straight away.
	c.send(fmt.Sprintf("S%08X|%s", r.guiHandle, r.guiClientStatus()))

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		logs.Add(r.tag, "<- %s %s", c.remote, line)
		r.handleLine(c, line)
	}
}

// handleLine answers one "C<seq>|<command>" line.
func (r *Radio) handleLine(c *apiClient, line string) {
	if line[0] != 'C' {
		return
	}
	bar := strings.IndexByte(line, '|')
	if bar < 0 {
		return
	}
	seq := strings.TrimPrefix(line[1:bar], "D") // "CD" is the debug form of "C"
	cmd := strings.TrimSpace(line[bar+1:])
	words := strings.Fields(cmd)
	if len(words) == 0 {
		return
	}
	ok := func(msg string) { c.send(fmt.Sprintf("R%s|0|%s", seq, msg)) }
	st := r.State()

	switch strings.ToLower(words[0]) {
	case "sub":
		if len(words) < 2 {
			ok("")
			return
		}
		obj := strings.ToLower(words[1])
		c.wmu.Lock()
		c.subs[obj] = true
		if obj == "radio" {
			c.subs["interlock"] = true
		}
		c.wmu.Unlock()
		ok("")
		switch obj {
		case "slice":
			r.status(c, r.sliceStatus(st))
		case "radio":
			r.status(c, r.radioStatus())
			r.status(c, r.interlockStatus(map[bool]string{true: "TRANSMITTING", false: "READY"}[st.PTT], st.PTT))
		case "tx":
			r.status(c, r.transmitStatus(st))
			r.status(c, r.interlockStatus(map[bool]string{true: "TRANSMITTING", false: "READY"}[st.PTT], st.PTT))
		case "client":
			r.status(c, r.guiClientStatus())
		}
	case "unsub":
		if len(words) >= 2 {
			c.wmu.Lock()
			delete(c.subs, strings.ToLower(words[1]))
			c.wmu.Unlock()
		}
		ok("")
	case "client":
		if len(words) >= 3 && strings.EqualFold(words[1], "program") {
			c.program.Store(strings.Join(words[2:], " "))
		}
		if len(words) >= 2 && strings.EqualFold(words[1], "gui") {
			ok("00000000-0000-0000-0000-000000000000")
			return
		}
		ok("")
	case "info":
		ok(fmt.Sprintf(`model="%s",chassis_serial="%s",name="%s",callsign="%s",gateway="0.0.0.0",ip=%s,location="",macaddr=00-1C-2D-00-00-01,netmask=255.255.255.0,num_scu=2,num_slice=4,num_tx=1,options="None",region="USA",screensaver="model",software_ver=%s`,
			r.cfg.Model, r.cfg.Serial, r.cfg.Nickname, r.cfg.Callsign, r.ip, r.cfg.Version))
	case "version":
		ok(fmt.Sprintf("SmartSDR-MB=%s#PSoC-MBTRX=3.0.0.0#PSoC-MBPA100=3.0.0.0#FPGA-MB=3.0.0.0", r.cfg.Version))
	case "slice":
		if len(words) >= 2 && strings.EqualFold(words[1], "list") {
			ok("0")
			return
		}
		ok("")
	case "ant":
		if len(words) >= 2 && strings.EqualFold(words[1], "list") {
			ok("ANT1,ANT2,RX_A,RX_B,XVTA,XVTB")
			return
		}
		ok("")
	default:
		// ping, keepalive, profile, display ... all just succeed.
		ok("")
	}
}
