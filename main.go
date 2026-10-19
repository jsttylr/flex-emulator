package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var appVersion = "0.2.0"

// Manager owns the running set of emulated radios and rebuilds it when settings change.
type Manager struct {
	mu     sync.Mutex
	store  *ConfigStore
	radios []*Radio
	errs   []string
	ip     string
}

func (m *Manager) Apply() {
	cfg := m.store.Get()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.radios {
		if r != nil {
			r.Stop()
		}
	}
	m.ip = cfg.BindIP
	if m.ip == "" {
		m.ip = localIP()
	}
	m.radios = make([]*Radio, len(cfg.Radios))
	m.errs = make([]string, len(cfg.Radios))
	for i, rc := range cfg.Radios {
		if !rc.Enabled {
			continue
		}
		r := NewRadio(i, rc, m.ip)
		if err := r.Start(); err != nil {
			m.errs[i] = err.Error()
			logs.Add(r.tag, "%v", err)
			continue
		}
		m.radios[i] = r
	}
}

func (m *Manager) Radio(i int) *Radio {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i < 0 || i >= len(m.radios) {
		return nil
	}
	return m.radios[i]
}

func (m *Manager) Statuses() []RadioStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []RadioStatus{}
	for i, r := range m.radios {
		if r == nil {
			out = append(out, RadioStatus{Index: i, Error: m.errs[i], Clients: []string{}})
			continue
		}
		out = append(out, r.Status(i))
	}
	return out
}

func main() {
	noBrowser := flag.Bool("no-browser", envBool("FLEXEMU_NO_BROWSER"), "don't open the settings page on start")
	cfgFile := flag.String("config", envOr("FLEXEMU_CONFIG", configPath()), "settings file")
	listen := flag.String("listen", os.Getenv("FLEXEMU_LISTEN"), "address for the settings page, e.g. 0.0.0.0:8992 (default: this computer only)")
	password := flag.String("password", os.Getenv("FLEXEMU_PASSWORD"), "password for the settings page (required when -listen is not this computer only)")
	healthcheck := flag.Bool("healthcheck", false, "check that a running emulator answers, then exit (for Docker)")
	flag.Parse()

	store, err := LoadConfig(*cfgFile)
	if err != nil {
		fatal("Flex Emulator can't read its settings file:\n%s\n\n%v", *cfgFile, err)
	}
	port := store.Get().WebPort
	addr := *listen
	if addr == "" {
		addr = fmt.Sprintf("127.0.0.1:%d", port)
	}
	server := !isLoopback(addr)
	if *healthcheck {
		os.Exit(healthCheck(addr, *password))
	}
	if server && *password == "" {
		fatal("The settings page is set to listen on %s, which other computers can reach, but no password is set.\nSet FLEXEMU_PASSWORD (or -password) and start again.", addr)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)

	// Already running? Just show its settings page again.
	if !server && alreadyRunning(port) {
		openBrowser(url)
		return
	}
	setupLogFile(filepath.Join(filepath.Dir(*cfgFile), "flex-emulator.log"))
	logs.Add("app", "Flex Emulator %s, settings in %s", appVersion, *cfgFile)

	m := &Manager{store: store}
	m.Apply()

	quit := make(chan struct{})
	opts := webOptions{addr: addr, password: *password, server: server}
	if err := startWeb(m, opts, quit); err != nil {
		fatal("Flex Emulator can't open its settings page on %s:\n%v", addr, err)
	}
	if server {
		logs.Add("app", "settings page on http://%s/ (password protected)", addr)
	} else {
		fmt.Printf("\nSettings page: %s\nLeave this window open. Close it to stop the emulator.\n\n", url)
		if !*noBrowser {
			openBrowser(url)
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-quit:
	}
	logs.Add("app", "stopping")
	m.StopAll()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	v, _ := strconv.ParseBool(os.Getenv(key))
	return v
}

// isLoopback reports whether addr ("host:port") only accepts connections from this computer.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.radios {
		if r != nil {
			r.Stop()
		}
	}
	m.radios = nil
}

func alreadyRunning(port int) bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// healthCheck asks the running emulator for its status and returns an exit code:
// 0 when it answers, 1 when it doesn't.
func healthCheck(addr, password string) int {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	req, _ := http.NewRequest("GET", "http://"+net.JoinHostPort(host, port)+"/api/status", nil)
	if password != "" {
		req.SetBasicAuth("healthcheck", password)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "not answering:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "settings page answered", resp.Status)
		return 1
	}
	return 0
}

// setupLogFile keeps a log next to the settings, since the Windows build has no console window.
func setupLogFile(path string) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.Rename(path, path+".old")
	f, err := os.Create(path)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(f, quietWriter{os.Stderr}))
}

// quietWriter ignores errors so a missing console never stops file logging.
type quietWriter struct{ w io.Writer }

func (q quietWriter) Write(p []byte) (int, error) { q.w.Write(p); return len(p), nil }

func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	showError(msg)
	os.Exit(1)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
