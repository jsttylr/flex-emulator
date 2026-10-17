package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync"
)

//go:embed web
var webFiles embed.FS

type webOptions struct {
	addr     string // listen address
	password string // empty means no login (only allowed on loopback)
	server   bool   // reachable from other computers
}

func startWeb(m *Manager, opts webOptions, quit chan struct{}) error {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFiles, "web")
	mux.Handle("/", http.FileServer(http.FS(static)))

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			writeJSON(w, m.store.Get())
		case http.MethodPost:
			var c Config
			if err := json.NewDecoder(req.Body).Decode(&c); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := m.store.Set(c); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			logs.Add("app", "settings saved, restarting radios")
			// Answer first; restarting can take a moment while inputs disconnect.
			writeJSON(w, map[string]string{"ok": "saved"})
			go safely("restart", m.Apply)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, req *http.Request) {
		m.mu.Lock()
		ip := m.ip
		m.mu.Unlock()
		writeJSON(w, map[string]any{"version": appVersion, "ip": ip, "server": opts.server, "radios": m.Statuses()})
	})

	mux.HandleFunc("/api/log", func(w http.ResponseWriter, req *http.Request) {
		since, _ := strconv.ParseInt(req.URL.Query().Get("since"), 10, 64)
		writeJSON(w, logs.Since(since))
	})

	mux.HandleFunc("/api/options", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, map[string]any{"serial_ports": serialPorts(), "ips": localIPs(), "auto_ip": localIP()})
	})

	var quitOnce sync.Once
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]string{"ok": "stopping"})
		quitOnce.Do(func() { close(quit) })
	})

	// Manual input: lets you test accessories without a radio connected.
	mux.HandleFunc("/api/manual", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Radio  int    `json:"radio"`
			FreqHz int64  `json:"freq_hz"`
			Mode   string `json:"mode"`
			PTT    *bool  `json:"ptt"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r := m.Radio(body.Radio)
		if r == nil {
			http.Error(w, "that radio isn't running", http.StatusNotFound)
			return
		}
		if r.cfg.Source.Type != "manual" {
			http.Error(w, "this radio follows a real rig; switch its input to Manual to test by hand", http.StatusConflict)
			return
		}
		r.update(func(st *RigState) {
			if body.FreqHz > 0 {
				st.FreqHz = body.FreqHz
			}
			if body.Mode != "" {
				st.Mode = normalizeMode(body.Mode)
			}
			if body.PTT != nil {
				st.PTT = *body.PTT
			}
		})
		writeJSON(w, r.State())
	})

	// By default only this computer can reach the page; server mode needs a password.
	ln, err := net.Listen("tcp", opts.addr)
	if err != nil {
		return err
	}
	go http.Serve(ln, guard(mux, opts))
	return nil
}

// guard protects the settings API. Changes always need a custom header, which forces a
// CORS preflight we never allow, so other web pages can't post to it. On this computer
// only, the Host must be local (stops DNS rebinding); in server mode a password is required.
func guard(h http.Handler, opts webOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if e := recover(); e != nil {
				logs.Add("app", "BUG in %s %s: %v\n%s", req.Method, req.URL.Path, e, debug.Stack())
				http.Error(w, fmt.Sprintf("internal error: %v", e), http.StatusInternalServerError)
			}
		}()
		if opts.password != "" {
			_, pass, ok := req.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(opts.password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Flex Emulator", charset="UTF-8"`)
				http.Error(w, "password required", http.StatusUnauthorized)
				return
			}
		}
		if !opts.server {
			host, _, err := net.SplitHostPort(req.Host)
			if err != nil {
				host = req.Host
			}
			if host != "127.0.0.1" && host != "localhost" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		if req.Method != http.MethodGet && req.Header.Get("X-Flex-Emu") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, req)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
