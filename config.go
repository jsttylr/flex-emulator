package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// SourceConfig describes where an emulated radio gets its frequency, mode and PTT from.
type SourceConfig struct {
	// Type is one of: manual, rigctld, civ, kenwood-serial, kenwood-tcp, tci.
	Type       string `json:"type"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	SerialPort string `json:"serial_port"`
	Baud       int    `json:"baud"`
	CIVAddress string `json:"civ_address"` // hex, e.g. "94" for IC-7300
	PollMs     int    `json:"poll_ms"`
}

// RadioConfig is one fake Flex radio that accessories will see on the network.
type RadioConfig struct {
	Enabled  bool         `json:"enabled"`
	Nickname string       `json:"nickname"`
	Callsign string       `json:"callsign"`
	Model    string       `json:"model"`
	Serial   string       `json:"serial"`
	Version  string       `json:"version"`
	APIPort  int          `json:"api_port"`
	RxAnt    string       `json:"rx_ant"`
	TxAnt    string       `json:"tx_ant"`
	Source   SourceConfig `json:"source"`
}

type Config struct {
	WebPort int `json:"web_port"`
	// BindIP is the address advertised to accessories. Empty means pick automatically.
	BindIP string        `json:"bind_ip"`
	Radios []RadioConfig `json:"radios"`
}

func defaultRadio() RadioConfig {
	return RadioConfig{
		Enabled:  true,
		Nickname: "IC-7300",
		Callsign: "",
		Model:    "FLEX-6600",
		Serial:   "9999-0000-7300-0001",
		Version:  "3.8.23.0",
		APIPort:  4992,
		RxAnt:    "ANT1",
		TxAnt:    "ANT1",
		Source: SourceConfig{
			Type:       "manual",
			Host:       "127.0.0.1",
			Port:       4532,
			Baud:       19200,
			CIVAddress: "94",
			PollMs:     250,
		},
	}
}

func defaultConfig() *Config {
	return &Config{WebPort: 8992, Radios: []RadioConfig{defaultRadio()}}
}

// fillDefaults patches zero values so older or hand-edited config files keep working.
func (c *Config) fillDefaults() {
	d := defaultRadio()
	if c.WebPort == 0 {
		c.WebPort = 8992
	}
	for i := range c.Radios {
		r := &c.Radios[i]
		if r.Model == "" {
			r.Model = d.Model
		}
		if r.Version == "" {
			r.Version = d.Version
		}
		if r.APIPort == 0 {
			r.APIPort = d.APIPort
		}
		if r.RxAnt == "" {
			r.RxAnt = d.RxAnt
		}
		if r.TxAnt == "" {
			r.TxAnt = d.TxAnt
		}
		if r.Source.Type == "" {
			r.Source.Type = "manual"
		}
		if r.Source.PollMs <= 0 {
			r.Source.PollMs = d.Source.PollMs
		}
		if r.Source.Baud == 0 {
			r.Source.Baud = d.Source.Baud
		}
		if r.Source.CIVAddress == "" {
			r.Source.CIVAddress = d.Source.CIVAddress
		}
	}
}

type ConfigStore struct {
	mu   sync.Mutex
	path string
	cfg  *Config
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "flex-emulator", "config.json")
}

func LoadConfig(path string) (*ConfigStore, error) {
	s := &ConfigStore{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.cfg = defaultConfig()
		return s, s.save()
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	c.fillDefaults()
	s.cfg = &c
	return s, nil
}

func (s *ConfigStore) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *s.cfg
	c.Radios = append([]RadioConfig(nil), s.cfg.Radios...)
	return c
}

func (s *ConfigStore) Set(c Config) error {
	c.fillDefaults()
	s.mu.Lock()
	s.cfg = &c
	s.mu.Unlock()
	return s.save()
}

func (s *ConfigStore) save() error {
	s.mu.Lock()
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}
