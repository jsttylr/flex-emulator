package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"go.bug.st/serial"
)

// Source reads frequency, mode and PTT from a real radio (or the app controlling it).
// Run blocks until the connection fails or ctx is cancelled.
type Source interface {
	Run(ctx context.Context, update func(func(*RigState))) error
}

func newSource(sc SourceConfig) (Source, error) {
	switch sc.Type {
	case "manual":
		return nil, nil
	case "rigctld":
		return &rigctldSource{cfg: sc}, nil
	case "civ":
		return &civSource{cfg: sc}, nil
	case "kenwood-serial", "kenwood-tcp":
		return &kenwoodSource{cfg: sc}, nil
	case "tci":
		return &tciSource{cfg: sc}, nil
	}
	return nil, fmt.Errorf("unknown input type %q", sc.Type)
}

func describeSource(sc SourceConfig) string {
	switch sc.Type {
	case "civ":
		return fmt.Sprintf("Icom CI-V on %s @ %d baud, address %s", sc.SerialPort, sc.Baud, sc.CIVAddress)
	case "kenwood-serial":
		return fmt.Sprintf("Kenwood CAT on %s @ %d baud", sc.SerialPort, sc.Baud)
	case "kenwood-tcp":
		return fmt.Sprintf("Kenwood CAT at %s:%d", sc.Host, sc.Port)
	case "rigctld":
		return fmt.Sprintf("rigctld at %s:%d", sc.Host, sc.Port)
	case "tci":
		return fmt.Sprintf("TCI at ws://%s:%d", sc.Host, sc.Port)
	}
	return sc.Type
}

func pollInterval(sc SourceConfig) time.Duration {
	ms := sc.PollMs
	if ms < 50 {
		ms = 250
	}
	return time.Duration(ms) * time.Millisecond
}

func dialTCP(ctx context.Context, host string, port int) (net.Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	return d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
}

func openSerial(sc SourceConfig) (serial.Port, error) {
	if sc.SerialPort == "" {
		return nil, fmt.Errorf("no serial port chosen")
	}
	p, err := serial.Open(sc.SerialPort, &serial.Mode{BaudRate: sc.Baud, DataBits: 8, StopBits: serial.OneStopBit, Parity: serial.NoParity})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", sc.SerialPort, err)
	}
	return p, nil
}

// closeOnDone closes c when ctx ends so blocked reads return.
func closeOnDone(ctx context.Context, c io.Closer) func() {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		c.Close()
	}()
	return func() { close(stop) }
}

func serialPorts() []string {
	ports, err := serial.GetPortsList()
	if err != nil || ports == nil {
		return []string{}
	}
	return ports
}

// normalizeMode maps the many radio/app mode spellings onto Flex names.
func normalizeMode(m string) string {
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case "LSB":
		return "LSB"
	case "USB":
		return "USB"
	case "CW", "CWR", "CW-R", "CWL", "CWU":
		return "CW"
	case "AM", "SAM", "DSB":
		return "AM"
	case "FM", "NFM", "WFM", "FMN":
		return "FM"
	case "PKTUSB", "DIGU", "USB-D", "DATA-U", "PKT-U":
		return "DIGU"
	case "PKTLSB", "DIGL", "LSB-D", "DATA-L", "PKT-L":
		return "DIGL"
	case "RTTY", "RTTYR", "RTTY-R", "FSK", "FSK-R":
		return "RTTY"
	}
	return "USB"
}
