package main

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"time"
)

// civSource talks Icom CI-V directly over the radio's USB serial port (IC-7300, IC-7610, IC-705 ...).
// Only use this when nothing else needs the port; otherwise point a rigctld or flrig at the radio.
type civSource struct{ cfg SourceConfig }

const civController = 0xE0

var civModes = map[byte]string{0x00: "LSB", 0x01: "USB", 0x02: "AM", 0x03: "CW", 0x04: "RTTY", 0x05: "FM", 0x07: "CW", 0x08: "RTTY", 0x06: "FM"}

func (s *civSource) Run(ctx context.Context, update func(func(*RigState))) error {
	addr64, err := strconv.ParseUint(s.cfg.CIVAddress, 16, 8)
	if err != nil {
		return fmt.Errorf("CI-V address %q is not a hex byte (IC-7300 is 94)", s.cfg.CIVAddress)
	}
	rig := byte(addr64)
	p, err := openSerial(s.cfg)
	if err != nil {
		return err
	}
	defer p.Close()
	defer closeOnDone(ctx, p)()

	errc := make(chan error, 1)
	got := make(chan struct{}, 1)
	go func() {
		rd := bufio.NewReader(p)
		for {
			frame, err := readCIVFrame(rd)
			if err != nil {
				errc <- err
				return
			}
			if handleCIV(frame, rig, update) {
				select {
				case got <- struct{}{}:
				default:
				}
			}
		}
	}()

	send := func(cmd ...byte) error {
		f := append([]byte{0xFE, 0xFE, rig, civController}, cmd...)
		_, err := p.Write(append(f, 0xFD))
		return err
	}
	t := time.NewTicker(pollInterval(s.cfg))
	defer t.Stop()
	lastReply := time.Now()
	for {
		for _, cmd := range [][]byte{{0x03}, {0x04}, {0x1C, 0x00}} {
			if err := send(cmd...); err != nil {
				return err
			}
			time.Sleep(20 * time.Millisecond) // give a shared CI-V bus room
		}
	wait:
		for {
			select {
			case <-ctx.Done():
				return nil
			case err := <-errc:
				return err
			case <-got:
				lastReply = time.Now()
			case <-t.C:
				break wait
			}
		}
		if time.Since(lastReply) > 5*time.Second {
			update(func(st *RigState) { st.Connected = false; st.PTT = false })
		}
	}
}

// readCIVFrame returns the bytes between FE FE and FD.
func readCIVFrame(rd *bufio.Reader) ([]byte, error) {
	for {
		b, err := rd.ReadByte()
		if err != nil {
			return nil, err
		}
		if b != 0xFE {
			continue
		}
		var frame []byte
		for {
			b, err := rd.ReadByte()
			if err != nil {
				return nil, err
			}
			if b == 0xFE && len(frame) == 0 {
				continue
			}
			if b == 0xFD {
				return frame, nil
			}
			frame = append(frame, b)
			if len(frame) > 64 {
				break // garbage; resync
			}
		}
	}
}

// handleCIV decodes frames sent by the rig: dst src cmd data...
func handleCIV(f []byte, rig byte, update func(func(*RigState))) bool {
	if len(f) < 3 || f[1] != rig {
		return false // our own echo or another device on the bus
	}
	cmd, data := f[2], f[3:]
	switch cmd {
	case 0x00, 0x03: // transceive frequency / read frequency
		if len(data) < 5 {
			return false
		}
		hz := bcdLE(data[:5])
		update(func(st *RigState) { st.Connected = true; st.FreqHz = hz })
		return true
	case 0x01, 0x04: // transceive mode / read mode
		if len(data) < 1 {
			return false
		}
		if m := civModes[data[0]]; m != "" {
			update(func(st *RigState) { st.Connected = true; st.Mode = m })
		}
		return true
	case 0x1C:
		if len(data) >= 2 && data[0] == 0x00 {
			tx := data[1] == 0x01
			update(func(st *RigState) { st.Connected = true; st.PTT = tx })
			return true
		}
	case 0xFB: // OK
		return true
	}
	return false
}

// bcdLE decodes Icom's little-endian packed BCD frequency.
func bcdLE(b []byte) int64 {
	var v, mul int64 = 0, 1
	for _, x := range b {
		v += int64(x&0x0F) * mul
		v += int64(x>>4) * mul * 10
		mul *= 100
	}
	return v
}
