package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
)

// tciSource listens to a TCI server (Thetis, ExpertSDR, AetherSDR, SunSDR).
// TCI pushes changes, so there is nothing to poll.
type tciSource struct{ cfg SourceConfig }

func (s *tciSource) Run(ctx context.Context, update func(func(*RigState))) error {
	url := fmt.Sprintf("ws://%s:%d", s.cfg.Host, s.cfg.Port)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer closeOnDone(ctx, conn.NetConn())()

	update(func(st *RigState) { st.Connected = true })
	tx, tune := false, false
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if kind != websocket.TextMessage {
			continue // audio / IQ streams
		}
		for _, msg := range strings.Split(string(data), ";") {
			name, args, ok := strings.Cut(strings.TrimSpace(msg), ":")
			if !ok {
				continue
			}
			a := strings.Split(args, ",")
			// Only follow receiver 0, VFO A.
			if len(a) == 0 || a[0] != "0" {
				continue
			}
			switch strings.ToLower(name) {
			case "vfo":
				if len(a) >= 3 && a[1] == "0" {
					if hz, err := strconv.ParseInt(a[2], 10, 64); err == nil {
						update(func(st *RigState) { st.FreqHz = hz })
					}
				}
			case "modulation":
				if len(a) >= 2 {
					m := normalizeMode(a[1])
					update(func(st *RigState) { st.Mode = m })
				}
			case "trx":
				if len(a) >= 2 {
					tx = strings.EqualFold(a[1], "true")
					p := tx || tune
					update(func(st *RigState) { st.PTT = p })
				}
			case "tune":
				if len(a) >= 2 {
					tune = strings.EqualFold(a[1], "true")
					p := tx || tune
					update(func(st *RigState) { st.PTT = p })
				}
			}
		}
	}
}
