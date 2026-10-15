package main

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// rigctldSource polls a hamlib rigctld (or anything speaking its protocol, e.g. flrig, SDR apps).
type rigctldSource struct{ cfg SourceConfig }

func (s *rigctldSource) Run(ctx context.Context, update func(func(*RigState))) error {
	conn, err := dialTCP(ctx, s.cfg.Host, s.cfg.Port)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer closeOnDone(ctx, conn)()
	rd := bufio.NewReader(conn)

	query := func(cmd string, lines int) ([]string, error) {
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := conn.Write([]byte(cmd + "\n")); err != nil {
			return nil, err
		}
		out := make([]string, 0, lines)
		for len(out) < lines {
			l, err := rd.ReadString('\n')
			if err != nil {
				return nil, err
			}
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "RPRT") {
				return nil, fmt.Errorf("rigctld %q answered %s", cmd, l)
			}
			out = append(out, l)
		}
		return out, nil
	}

	first := true
	t := time.NewTicker(pollInterval(s.cfg))
	defer t.Stop()
	for {
		f, err := query("f", 1)
		if err != nil {
			return err
		}
		hz, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			return fmt.Errorf("rigctld sent a frequency I can't read: %q", f[0])
		}
		m, err := query("m", 2)
		if err != nil {
			return err
		}
		ptt := false
		if p, err := query("t", 1); err == nil {
			ptt = p[0] == "1"
		}
		update(func(st *RigState) {
			st.Connected = true
			st.FreqHz = int64(hz)
			st.Mode = normalizeMode(m[0])
			st.PTT = ptt
		})
		if first {
			logs.Add("input", "rigctld connected")
			first = false
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
