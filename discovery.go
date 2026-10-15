package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"time"
)

// FlexRadio discovery is a VITA-49 "extension data with stream ID" packet broadcast
// to UDP 4992 about once a second. The payload is a space separated key=value string.
const (
	discoveryPort     = 4992
	discoveryStreamID = 0x00000800
	flexOUI           = 0x00001C2D
	discoveryClassID  = 0x534CFFFF // information class 0x534C, packet class 0xFFFF

	vitaTypeExtDataWithStream = 0x3
	vitaClassIDPresent        = 1 << 27
	vitaTSIOther              = 0x3 << 22
	vitaTSFSampleCount        = 0x1 << 20
)

func buildDiscoveryPacket(count uint32, payload string) []byte {
	body := []byte(payload)
	// Pad to a 32-bit boundary with at least one zero byte, as the radio does.
	pad := 4 - len(body)%4
	body = append(body, make([]byte, pad)...)

	const headerWords = 7
	words := headerWords + len(body)/4
	header := uint32(vitaTypeExtDataWithStream)<<28 | vitaClassIDPresent | vitaTSIOther | vitaTSFSampleCount |
		(count&0xF)<<16 | uint32(words)&0xFFFF

	pkt := make([]byte, headerWords*4, words*4)
	binary.BigEndian.PutUint32(pkt[0:], header)
	binary.BigEndian.PutUint32(pkt[4:], discoveryStreamID)
	binary.BigEndian.PutUint32(pkt[8:], flexOUI)
	binary.BigEndian.PutUint32(pkt[12:], discoveryClassID)
	// timestamps (int, frac high, frac low) stay zero
	return append(pkt, body...)
}

// discoveryPayload mirrors the keys a current SmartSDR v3 radio sends.
func discoveryPayload(rc RadioConfig, ip string) string {
	sp := func(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), " ", "_") }
	kv := [][2]string{
		{"discovery_protocol_version", "3.0.0.2"},
		{"model", sp(rc.Model)},
		{"serial", sp(rc.Serial)},
		{"version", sp(rc.Version)},
		{"nickname", sp(rc.Nickname)},
		{"callsign", sp(rc.Callsign)},
		{"ip", ip},
		{"port", fmt.Sprint(rc.APIPort)},
		{"status", "Available"},
		{"inuse_ip", ""},
		{"inuse_host", ""},
		{"max_licensed_version", "v3"},
		{"radio_license_id", "00-1C-2D-00-00-01"},
		{"requires_additional_license", "0"},
		{"fpc_mac", ""},
		{"wan_connected", "0"},
		{"licensed_clients", "2"},
		{"available_clients", "2"},
		{"max_panadapters", "4"},
		{"available_panadapters", "4"},
		{"max_slices", "4"},
		{"available_slices", "4"},
		{"gui_client_ips", ""},
		{"gui_client_hosts", ""},
		{"gui_client_programs", ""},
		{"gui_client_stations", ""},
		{"gui_client_handles", ""},
		{"min_software_version", "3.0.0.0"},
		{"external_port_link", "0"},
	}
	parts := make([]string, len(kv))
	for i, p := range kv {
		parts[i] = p[0] + "=" + p[1]
	}
	return strings.Join(parts, " ")
}

// runDiscovery broadcasts the radio until ctx is cancelled.
func runDiscovery(ctx context.Context, tag string, rc RadioConfig, ip string) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(ip)})
	if err != nil {
		logs.Add(tag, "discovery: cannot open UDP socket on %s: %v", ip, err)
		return
	}
	defer conn.Close()

	targets := []*net.UDPAddr{{IP: net.IPv4bcast, Port: discoveryPort}}
	if b := subnetBroadcast(ip); b != nil {
		targets = append(targets, &net.UDPAddr{IP: b, Port: discoveryPort})
	}
	payload := discoveryPayload(rc, ip)
	logs.Add(tag, "discovery: broadcasting as %q (%s) at %s:%d", rc.Nickname, rc.Model, ip, rc.APIPort)

	var count uint32
	t := time.NewTicker(time.Second)
	defer t.Stop()
	warned := false
	for {
		pkt := buildDiscoveryPacket(count, payload)
		count++
		for _, dst := range targets {
			if _, err := conn.WriteToUDP(pkt, dst); err != nil && !warned {
				logs.Add(tag, "discovery: send to %s failed: %v", dst, err)
				warned = true
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// localIP picks the address of the interface used to reach the LAN.
func localIP() string {
	c, err := net.Dial("udp4", "192.0.2.1:9") // no packets are sent
	if err == nil {
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).IP.String()
	}
	for _, a := range localIPs() {
		return a
	}
	return "127.0.0.1"
}

// localIPs lists the machine's IPv4 addresses for the settings page.
func localIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			out = append(out, n.IP.String())
		}
	}
	return out
}

func subnetBroadcast(ip string) net.IP {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.String() != ip {
			continue
		}
		ip4, mask := n.IP.To4(), n.Mask
		if len(mask) == 16 {
			mask = mask[12:]
		}
		if ip4 == nil || len(mask) != 4 {
			return nil
		}
		b := make(net.IP, 4)
		for i := range b {
			b[i] = ip4[i] | ^mask[i]
		}
		return b
	}
	return nil
}
