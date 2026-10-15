package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var appVersion = "0.1.1"

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
	noBrowser := flag.Bool("no-browser", false, "don't open the settings page on start")
	cfgFile := flag.String("config", configPath(), "settings file")
	flag.Parse()

	store, err := LoadConfig(*cfgFile)
	if err != nil {
		fatal("Flex Emulator can't read its settings file:\n%s\n\n%v", *cfgFile, err)
	}
	port := store.Get().WebPort
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)

	// Already running? Just show its settings page again.
	if alreadyRunning(port) {
		openBrowser(url)
		return
	}
	setupLogFile(filepath.Join(filepath.Dir(*cfgFile), "flex-emulator.log"))
	logs.Add("app", "Flex Emulator %s, settings in %s", appVersion, *cfgFile)

	m := &Manager{store: store}
	m.Apply()

	quit := make(chan struct{})
	if _, err := startWeb(m, port, quit); err != nil {
		fatal("Flex Emulator can't open its settings page on port %d:\n%v", port, err)
	}
	fmt.Printf("\nSettings page: %s\nLeave this window open. Close it to stop the emulator.\n\n", url)
	if !*noBrowser {
		openBrowser(url)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case <-sig:
	case <-quit:
	}
	logs.Add("app", "stopping")
	m.StopAll()
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
