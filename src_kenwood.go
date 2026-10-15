package main

import (
	"bufio"
	"context"
	"io"
	"strconv"
	"time"
)

// kenwoodSource speaks Kenwood TS-2000 style CAT ("IF;"), which Thetis, many SDR apps
// and Kenwood/Elecraft/Yaesu-in-Kenwood-mode radios understand, over serial or TCP.
type kenwoodSource struct{ cfg SourceConfig }

func (s *kenwoodSource) Run(ctx context.Context, update func(func(*RigState))) error {
	var rw io.ReadWriteCloser
	if s.cfg.Type == "kenwood-tcp" {
		c, err := dialTCP(ctx, s.cfg.Host, s.cfg.Port)
		if err != nil {
			return err
		}
		rw = c
	} else {
		p, err := openSerial(s.cfg)
		if err != nil {
			return err
		}
		rw = p
	}
	defer rw.Close()
	defer closeOnDone(ctx, rw)()

	errc := make(chan error, 1)
	got := make(chan struct{}, 1)
	go func() {
		rd := bufio.NewReader(rw)
		for {
			msg, err := rd.ReadString(';')
			if err != nil {
				errc <- err
				return
			}
			if parseKenwood(msg, update) {
				select {
				case got <- struct{}{}:
				default:
				}
			}
		}
	}()

	t := time.NewTicker(pollInterval(s.cfg))
	defer t.Stop()
	lastReply := time.Now()
	for {
		if _, err := rw.Write([]byte("IF;")); err != nil {
			return err
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

var kenwoodModes = map[byte]string{'1': "LSB", '2': "USB", '3': "CW", '4': "FM", '5': "AM", '6': "RTTY", '7': "CW", '9': "RTTY"}

// parseKenwood handles IF, FA and MD answers. It reports whether anything useful was read.
func parseKenwood(msg string, update func(func(*RigState))) bool {
	// strip anything before the command (line noise, echoes)
	for len(msg) > 2 && !(msg[0] >= 'A' && msg[0] <= 'Z' && msg[1] >= 'A' && msg[1] <= 'Z') {
		msg = msg[1:]
	}
	if len(msg) < 3 {
		return false
	}
	switch msg[:2] {
	case "IF":
		// IF + freq(11) + 5 spaces + rit(5) + rit,xit,bank(3) + ch(2), so TX/RX is index 28 and mode 29 (as hamlib reads it).
		if len(msg) < 31 {
			return false
		}
		hz, err := strconv.ParseInt(msg[2:13], 10, 64)
		if err != nil {
			return false
		}
		tx := msg[28] == '1'
		mode := kenwoodModes[msg[29]]
		update(func(st *RigState) {
			st.Connected = true
			st.FreqHz = hz
			st.PTT = tx
			if mode != "" {
				st.Mode = mode
			}
		})
		return true
	case "FA":
		if len(msg) < 14 {
			return false
		}
		hz, err := strconv.ParseInt(msg[2:13], 10, 64)
		if err != nil {
			return false
		}
		update(func(st *RigState) { st.Connected = true; st.FreqHz = hz })
		return true
	case "MD":
		if mode := kenwoodModes[msg[2]]; mode != "" {
			update(func(st *RigState) { st.Connected = true; st.Mode = mode })
			return true
		}
	}
	return false
}
