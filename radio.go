package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"
)

// RigState is what we know about the real radio behind an emulated Flex.
type RigState struct {
	FreqHz    int64  `json:"freq_hz"`
	Mode      string `json:"mode"` // Flex mode names: USB, LSB, CW, AM, FM, DIGU, DIGL, RTTY
	PTT       bool   `json:"ptt"`
	Connected bool   `json:"connected"` // the input source is talking to the rig
}

// Radio is one emulated Flex: a discovery broadcaster, an API server and an input source.
type Radio struct {
	tag       string
	cfg       RadioConfig
	ip        string
	guiHandle uint32 // the pretend SmartSDR client that owns slice A

	mu      sync.Mutex
	state   RigState
	clients map[*apiClient]struct{}
	srcErr  string

	cancel context.CancelFunc
	done   sync.WaitGroup
}

func NewRadio(index int, rc RadioConfig, ip string) *Radio {
	return &Radio{
		tag:       fmt.Sprintf("radio%d", index+1),
		cfg:       rc,
		ip:        ip,
		guiHandle: 0x10000000 | rand.Uint32()&0x0FFFFFFF,
		state:     RigState{FreqHz: 14074000, Mode: "USB"},
		clients:   map[*apiClient]struct{}{},
	}
}

func (r *Radio) Start() error {
	ln, err := net.Listen("tcp4", fmt.Sprintf("%s:%d", r.ip, r.cfg.APIPort))
	if err != nil {
		// Fall back to all interfaces; the advertised IP still points at us.
		ln, err = net.Listen("tcp4", fmt.Sprintf(":%d", r.cfg.APIPort))
		if err != nil {
			return fmt.Errorf("cannot listen on TCP port %d (is another program, or another emulated radio, using it?): %w", r.cfg.APIPort, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	r.done.Add(3)
	go func() { defer r.done.Done(); safely("discovery", func() { runDiscovery(ctx, r.tag, r.cfg, r.ip) }) }()
	go func() { defer r.done.Done(); safely("api", func() { r.serveAPI(ctx, ln) }) }()
	go func() { defer r.done.Done(); safely("input", func() { r.runSource(ctx) }) }()
	return nil
}

func (r *Radio) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Lock()
	for c := range r.clients {
		c.conn.Close()
	}
	r.mu.Unlock()
	r.done.Wait()
}

func (r *Radio) runSource(ctx context.Context) {
	src, err := newSource(r.cfg.Source)
	if err != nil {
		r.setSourceError(err.Error())
		logs.Add(r.tag, "input: %v", err)
		return
	}
	if r.cfg.Source.Type == "manual" {
		r.update(func(s *RigState) { s.Connected = true })
		<-ctx.Done()
		return
	}
	// Reconnect forever; radios get switched off and apps get restarted.
	for {
		logs.Add(r.tag, "input: connecting (%s)", describeSource(r.cfg.Source))
		err := src.Run(ctx, r.update)
		r.update(func(s *RigState) { s.Connected = false; s.PTT = false })
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			r.setSourceError(err.Error())
			logs.Add(r.tag, "input: %v (retrying in 3s)", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (r *Radio) setSourceError(e string) {
	r.mu.Lock()
	r.srcErr = e
	r.mu.Unlock()
}

// update applies a change from the input source and pushes Flex status to accessories.
func (r *Radio) update(f func(*RigState)) {
	r.mu.Lock()
	old := r.state
	f(&r.state)
	now := r.state
	if now.Connected {
		r.srcErr = ""
	}
	r.mu.Unlock()

	if now.FreqHz != old.FreqHz || now.Mode != old.Mode {
		r.broadcast("slice", r.sliceStatus(now))
	}
	if now.PTT != old.PTT {
		if now.PTT {
			r.broadcast("interlock", r.interlockStatus("PTT_REQUESTED", true))
			r.broadcast("interlock", r.interlockStatus("TRANSMITTING", true))
		} else {
			r.broadcast("interlock", r.interlockStatus("UNKEY_REQUESTED", false))
			r.broadcast("interlock", r.interlockStatus("READY", false))
		}
		r.broadcast("tx", r.transmitStatus(now))
	}
	if now.FreqHz != old.FreqHz || now.PTT != old.PTT || now.Connected != old.Connected {
		logs.Add(r.tag, "rig: %s %s %s", fmtMHz(now.FreqHz), now.Mode, map[bool]string{true: "TX", false: "RX"}[now.PTT])
	}
}

func (r *Radio) State() RigState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

type RadioStatus struct {
	Index    int      `json:"index"`
	Nickname string   `json:"nickname"`
	IP       string   `json:"ip"`
	Port     int      `json:"port"`
	State    RigState `json:"state"`
	Error    string   `json:"error"`
	Clients  []string `json:"clients"`
}

func (r *Radio) Status(i int) RadioStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := RadioStatus{Index: i, Nickname: r.cfg.Nickname, IP: r.ip, Port: r.cfg.APIPort, State: r.state, Error: r.srcErr, Clients: []string{}}
	for c := range r.clients {
		name := c.remote
		if p, _ := c.program.Load().(string); p != "" {
			name = p + " (" + c.remote + ")"
		}
		st.Clients = append(st.Clients, name)
	}
	return st
}

func fmtMHz(hz int64) string { return fmt.Sprintf("%.6f", float64(hz)/1e6) }

// --- Flex status lines ---------------------------------------------------

func (r *Radio) sliceStatus(s RigState) string {
	mode := s.Mode
	if mode == "" {
		mode = "USB"
	}
	lo, hi := filterFor(mode)
	return strings.Join([]string{
		"slice 0",
		"in_use=1",
		"sample_rate=24000",
		"RF_frequency=" + fmtMHz(s.FreqHz),
		fmt.Sprintf("client_handle=0x%08X", r.guiHandle),
		"index_letter=A",
		"rit_on=0 rit_freq=0 xit_on=0 xit_freq=0",
		"rxant=" + r.cfg.RxAnt,
		"mode=" + mode,
		"wide=0",
		fmt.Sprintf("filter_lo=%d filter_hi=%d", lo, hi),
		"step=10 step_list=1,10,50,100,500,1000,2000,3000",
		"agc_mode=med agc_threshold=65 agc_off_level=10",
		"pan=0x40000000",
		"txant=" + r.cfg.TxAnt,
		"loopa=0 loopb=0 qsk=0 dax=0 dax_clients=0 lock=0",
		"tx=1 active=1",
		"audio_level=50 audio_pan=50 audio_mute=0",
		"record=0 play=disabled record_time=0.0",
		"ant_list=ANT1,ANT2,RX_A,RX_B,XVTA,XVTB tx_ant_list=ANT1,ANT2,XVTA,XVTB",
		"mode_list=LSB,USB,AM,CW,DIGL,DIGU,SAM,FM,NFM,DFM,RTTY",
		"diversity=0 diversity_parent=0 diversity_child=0 diversity_index=0",
		"detached=0",
	}, " ")
}

func filterFor(mode string) (int, int) {
	switch mode {
	case "LSB", "DIGL":
		return -2900, -100
	case "CW":
		return -200, 200
	case "AM", "SAM", "FM", "NFM", "DFM":
		return -3000, 3000
	default:
		return 100, 2900
	}
}

func (r *Radio) interlockStatus(state string, tx bool) string {
	src, handle := "", uint32(0)
	if tx {
		src, handle = "SW", r.guiHandle
	}
	return fmt.Sprintf("interlock acc_txreq_enable=0 rca_txreq_enable=0 acc_tx_enabled=1 tx1_enabled=1 tx2_enabled=1 tx3_enabled=1 "+
		"tx_delay=0 acc_tx_delay=0 tx1_delay=0 tx2_delay=0 tx3_delay=0 acc_txreq_polarity=0 rca_txreq_polarity=0 timeout=0 "+
		"state=%s tx_allowed=1 source=%s reason= tx_client_handle=0x%08X amplifier= ", state, src, handle)
}

func (r *Radio) transmitStatus(s RigState) string {
	return fmt.Sprintf("transmit freq=%s rfpower=100 tunepower=10 tune=0 mox=%d hwalc_enabled=0 inhibit=0 max_power_level=100",
		fmtMHz(s.FreqHz), b2i(s.PTT))
}

func (r *Radio) radioStatus() string {
	nick := strings.ReplaceAll(r.cfg.Nickname, " ", "_")
	call := strings.ReplaceAll(r.cfg.Callsign, " ", "_")
	return fmt.Sprintf("radio slices=3 panadapters=3 lineout_gain=60 lineout_mute=0 headphone_gain=50 headphone_mute=0 "+
		"remote_on_enabled=0 pll_done=0 freq_error_ppb=0 cal_freq=15.000000 tnf_enabled=0 nickname=%s callsign=%s "+
		"binaural_rx=0 full_duplex_enabled=0 band_persistence_enabled=1 rtty_mark_default=2125 enforce_private_ip_connections=1 "+
		"backlight=50 mute_local_audio_when_remote=1 daxiq_capacity=16 daxiq_available=16", nick, call)
}

func (r *Radio) guiClientStatus() string {
	return fmt.Sprintf("client 0x%08X connected local_ptt=1 client_id=00000000-0000-0000-0000-%012X program=FlexEmulator station=%s",
		r.guiHandle, r.guiHandle, strings.ReplaceAll(r.cfg.Nickname, " ", "_"))
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
