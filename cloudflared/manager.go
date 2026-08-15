package cloudflared

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"
)

type Config struct {
	Port        int
	Host        string
	MetricsPort int
	TunnelID    string
	ConfigPath  string
	WorkDir     string
	KeepAlive   bool
}

type Manager struct {
	config           Config
	cmd              *exec.Cmd
	cmdMutex         sync.Mutex
	tunnelURL        string
	lastStarted      time.Time
	keepAliveRunning bool
	stopChan         chan struct{}
}

type platformInfo struct {
	command string
	url     string
}

var configMap = map[string]map[string]platformInfo{
	"windows": {
		"amd64": {"cloudflared-windows-amd64.exe", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"},
		"386":   {"cloudflared-windows-386.exe", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-386.exe"},
	},
	"linux": {
		"amd64": {"cloudflared-linux-amd64", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64"},
		"386":   {"cloudflared-linux-386", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-386"},
		"arm":   {"cloudflared-linux-arm", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm"},
		"arm64": {"cloudflared-linux-arm64", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm64"},
	},
	"darwin": {
		"amd64": {"cloudflared", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-darwin-amd64.tgz"},
		"arm64": {"cloudflared", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-darwin-amd64.tgz"},
	},
}

func NewManager(cfg Config) *Manager {
	if cfg.MetricsPort == 0 {
		cfg.MetricsPort = int(8100 + time.Now().UnixNano()%900)
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = os.TempDir()
	}

	m := &Manager{
		config:   cfg,
		stopChan: make(chan struct{}),
	}

	if cfg.KeepAlive {
		m.keepAliveRunning = true
		go m.keepAliveLoop()
	}

	return m
}

func (m *Manager) getPlatformInfo() (platformInfo, error) {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	p, ok := configMap[osName]
	if !ok {
		return platformInfo{}, fmt.Errorf("unsupported OS: %s", osName)
	}

	info, ok := p[arch]
	if !ok {
		return platformInfo{}, fmt.Errorf("unsupported architecture %s on %s", arch, osName)
	}

	return info, nil
}

func (m *Manager) EnsureCloudflared() (string, error) {
	if path, err := exec.LookPath("cloudflared"); err == nil {
		return path, nil
	}

	pInfo, err := m.getPlatformInfo()
	if err != nil {
		return "", err
	}

	execPath := filepath.Join(m.config.WorkDir, pInfo.command)
	if _, err := os.Stat(execPath); err == nil {
		return execPath, nil
	}

	log.Printf(" * Downloading cloudflared for %s %s...", runtime.GOOS, runtime.GOARCH)
	if err := m.downloadFile(pInfo.url, execPath); err != nil {
		return "", err
	}

	if err := os.Chmod(execPath, 0755); err != nil {
		return "", err
	}

	return execPath, nil
}

func (m *Manager) downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func (m *Manager) Start() (string, error) {
	m.cmdMutex.Lock()
	defer m.cmdMutex.Unlock()

	_ = m.stopLocked()

	execPath, err := m.EnsureCloudflared()
	if err != nil {
		return "", err
	}

	args := []string{"tunnel", "--metrics", fmt.Sprintf("127.0.0.1:%d", m.config.MetricsPort)}
	if m.config.ConfigPath != "" {
		args = append(args, "--config", m.config.ConfigPath, "run")
	} else if m.config.TunnelID != "" {
		args = append(args, "--url", fmt.Sprintf("http://%s:%d", m.config.Host, m.config.Port), "run", m.config.TunnelID)
	} else {
		args = append(args, "--url", fmt.Sprintf("http://%s:%d", m.config.Host, m.config.Port))
	}

	m.cmd = exec.Command(execPath, args...)
	m.cmd.Stdout = nil
	m.cmd.Stderr = nil

	if err := m.cmd.Start(); err != nil {
		return "", err
	}

	m.lastStarted = time.Now()

	metricsURL := fmt.Sprintf("http://127.0.0.1:%d/metrics", m.config.MetricsPort)
	var tunnelURL string

	for i := 0; i < 15; i++ {
		time.Sleep(2 * time.Second)
		resp, err := http.Get(metricsURL)
		if err != nil {
			continue
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		metricsText := string(bodyBytes)

		if m.config.TunnelID != "" || m.config.ConfigPath != "" {
			matched, _ := regexp.MatchString(`cloudflared_tunnel_ha_connections\s+\d`, metricsText)
			if matched {
				tunnelURL = "preconfigured tunnel URL"
				break
			}
		} else {
			re := regexp.MustCompile(`(?P<url>https?://[^\s]+?\.trycloudflare\.com)`)
			match := re.FindStringSubmatch(metricsText)
			if len(match) > 1 {
				tunnelURL = match[1]
				break
			}
		}
	}

	if tunnelURL == "" {
		m.stopLocked()
		return "", fmt.Errorf("can't connect to Cloudflare Edge")
	}

	m.tunnelURL = tunnelURL
	
	urlFile := filepath.Join(os.TempDir(), "quickflare.url")
	_ = os.WriteFile(urlFile, []byte(tunnelURL), 0644)

	log.Printf(" * Running on %s", tunnelURL)
	log.Printf(" * Traffic stats available on %s", metricsURL)
	return tunnelURL, nil
}

func (m *Manager) Stop() error {
	m.cmdMutex.Lock()
	defer m.cmdMutex.Unlock()
	return m.stopLocked()
}

func (m *Manager) stopLocked() error {
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
		m.cmd = nil
	}
	m.tunnelURL = ""
	_ = os.Remove(filepath.Join(os.TempDir(), "quickflare.url"))
	return nil
}

func (m *Manager) Restart() (string, error) {
	_ = m.Stop()
	return m.Start()
}

func (m *Manager) checkSourceState() bool {
	resp, err := http.Get(fmt.Sprintf("http://%s:%d", m.config.Host, m.config.Port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *Manager) checkDestinationState() bool {
	if m.tunnelURL == "" || m.tunnelURL == "preconfigured tunnel URL" {
		return true
	}
	resp, err := http.Get(m.tunnelURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *Manager) keepAliveLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	counter := 0
	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			if !m.keepAliveRunning {
				return
			}
			counter++
			if counter >= 300 {
				counter = 0
				if time.Since(m.lastStarted) >= 5*time.Minute {
					if m.checkSourceState() && !m.checkDestinationState() {
						log.Println(" * Destination tunnel down. Restarting...")
						_, _ = m.Restart()
					}
				}
			}
		}
	}
}

func (m *Manager) Close() {
	m.keepAliveRunning = false
	close(m.stopChan)
	_ = m.Stop()
}
