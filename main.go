package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tna76874/quickflare-go/cloudflared"
)

const pidFileName = "quickflare.pid"
const logFileName = "quickflare.log"
const urlFileName = "quickflare.url"

func getPidFilePath() string {
	return filepath.Join(os.TempDir(), pidFileName)
}

func getLogFilePath() string {
	return filepath.Join(os.TempDir(), logFileName)
}

func getUrlFilePath() string {
	return filepath.Join(os.TempDir(), urlFileName)
}

func isRunning() (int, bool) {
	data, err := os.ReadFile(getPidFilePath())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, false
	}
	process, err := os.FindProcess(pid)
	if err != nil || process.Signal(syscall.Signal(0)) != nil {
		_ = os.Remove(getPidFilePath())
		_ = os.Remove(getUrlFilePath())
		return 0, false
	}
	return pid, true
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "quickflare-go",
		Short: "Quickflare-go verwaltet Cloudflare Tunnels über eine zentrale CLI",
	}

	var port int
	var host string
	var metricsPort int
	var tunnelID string
	var configPath string
	var workPath string
	var keepAlive bool
	var daemon bool
	var foregroundChild bool

	var startCmd = &cobra.Command{
		Use:   "start",
		Short: "Startet den Cloudflare Tunnel (Singleton)",
		Run: func(cmd *cobra.Command, args []string) {
			if !foregroundChild {
				if pid, active := isRunning(); active {
					fmt.Printf("Fehler: Ein Quickflare-Tunnel läuft bereits (PID: %d).\n", pid)
					os.Exit(1)
				}
			}

			cfg := cloudflared.Config{
				Port:        port,
				Host:        host,
				MetricsPort: metricsPort,
				TunnelID:    tunnelID,
				ConfigPath:  configPath,
				WorkDir:     workPath,
				KeepAlive:   keepAlive,
			}

			if daemon && !foregroundChild {
				logPath := getLogFilePath()
				logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
				if err != nil {
					log.Fatalf("Fehler beim Erstellen der Logdatei: %v", err)
				}

				var subArgs []string
				for _, arg := range os.Args[1:] {
					if arg != "-d" && arg != "--daemon" && arg != "-daemon" {
						subArgs = append(subArgs, arg)
					}
				}
				subArgs = append(subArgs, "--foreground-child")

				executable, err := os.Executable()
				if err != nil {
					executable = os.Args[0]
				}

				cmd := exec.Command(executable, subArgs...)
				cmd.Stdout = logFile
				cmd.Stderr = logFile
				cmd.Stdin = nil
				cmd.SysProcAttr = &syscall.SysProcAttr{
					Setsid: true,
				}

				if err := cmd.Start(); err != nil {
					log.Fatalf("Fehler beim Starten des Daemon-Prozesses: %v", err)
				}

				pid := cmd.Process.Pid
				_ = os.WriteFile(getPidFilePath(), []byte(strconv.Itoa(pid)), 0644)
				_ = cmd.Process.Release()

				fmt.Printf(" * Quickflare Daemon gestartet (PID: %d). Warte auf Tunnel-URL...\n", pid)

				for i := 0; i < 30; i++ {
					time.Sleep(1 * time.Second)
					if data, err := os.ReadFile(getUrlFilePath()); err == nil && len(data) > 0 {
						fmt.Printf(" * Tunnel erfolgreich verbunden: %s\n", string(data))
						return
					}
					if _, active := isRunning(); !active {
						fmt.Println("Fehler: Daemon-Prozess wurde unerwartet beendet. Prüfe die Logs mit:")
						fmt.Println("        ./dist/quickflare-go logs")
						os.Exit(1)
					}
				}

				fmt.Println("Warnung: Timeout beim Warten auf die URL. Prüfe './dist/quickflare-go logs'.")
				return
			}

			// Vordergrund-Modus / Daemon-Kindprozess
			pid := os.Getpid()
			_ = os.WriteFile(getPidFilePath(), []byte(strconv.Itoa(pid)), 0644)
			_ = os.Remove(getUrlFilePath())

			manager := cloudflared.NewManager(cfg)
			defer manager.Close()

			url, err := manager.Start()
			if err != nil {
				log.Fatalf("Fehler beim Starten von cloudflared: %v", err)
			}

			_ = os.WriteFile(getUrlFilePath(), []byte(url), 0644)

			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			<-sigChan

			_ = os.Remove(getPidFilePath())
			_ = os.Remove(getUrlFilePath())
		},
	}

	startCmd.Flags().IntVarP(&port, "port", "p", 0, "Lokaler Port (zwingend erforderlich)")
	startCmd.Flags().StringVarP(&host, "host", "H", "", "Lokaler Host (zwingend erforderlich)")
	startCmd.Flags().IntVar(&metricsPort, "metrics-port", 0, "Metrics Port für cloudflared")
	startCmd.Flags().StringVar(&tunnelID, "tunnel-id", "", "Cloudflare Tunnel ID")
	startCmd.Flags().StringVar(&configPath, "config-path", "", "Pfad zur cloudflared Konfigurationsdatei")
	startCmd.Flags().StringVar(&workPath, "path", os.TempDir(), "Standard-Download-Pfad für cloudflared")
	startCmd.Flags().BoolVar(&keepAlive, "keep-alive", true, "Keep-alive & Auto-Restart aktivieren")
	startCmd.Flags().BoolVarP(&daemon, "daemon", "d", false, "Im Hintergrund (Daemon) ausführen")
	startCmd.Flags().BoolVar(&foregroundChild, "foreground-child", false, "Interner Flag für Daemon-Kindprozess")
	_ = startCmd.Flags().MarkHidden("foreground-child")

	// Host und Port als Pflichtfelder definieren
	_ = startCmd.MarkFlagRequired("port")
	_ = startCmd.MarkFlagRequired("host")

	var stopCmd = &cobra.Command{
		Use:   "stop",
		Short: "Stoppt den laufenden Quickflare-Daemon",
		Run: func(cmd *cobra.Command, args []string) {
			pid, active := isRunning()
			if !active {
				fmt.Println("Kein laufender Quickflare-Daemon gefunden.")
				_ = os.Remove(getPidFilePath())
				_ = os.Remove(getUrlFilePath())
				return
			}

			process, err := os.FindProcess(pid)
			if err == nil {
				_ = process.Signal(syscall.SIGTERM)
			}

			_ = os.Remove(getPidFilePath())
			_ = os.Remove(getUrlFilePath())
			fmt.Printf(" * Quickflare (PID %d) wurde gestoppt.\n", pid)
		},
	}

	var statusCmd = &cobra.Command{
		Use:   "status",
		Short: "Prüft den Status des Quickflare-Daemons",
		Run: func(cmd *cobra.Command, args []string) {
			pid, active := isRunning()
			if !active {
				fmt.Println("Quickflare läuft nicht.")
				os.Exit(1)
			}
			fmt.Printf(" * Quickflare läuft im Hintergrund (PID: %d)\n", pid)
		},
	}

	var urlCmd = &cobra.Command{
		Use:   "url",
		Short: "Gibt die aktuelle Tunnel-URL aus",
		Run: func(cmd *cobra.Command, args []string) {
			_, active := isRunning()
			if !active {
				os.Exit(1)
			}

			data, err := os.ReadFile(getUrlFilePath())
			if err != nil || len(data) == 0 {
				os.Exit(1)
			}

			fmt.Println(string(data))
		},
	}

	var logsCmd = &cobra.Command{
		Use:   "logs",
		Short: "Gibt die Logs des Daemons aus",
		Run: func(cmd *cobra.Command, args []string) {
			data, err := os.ReadFile(getLogFilePath())
			if err != nil {
				fmt.Println("Keine Log-Datei gefunden.")
				return
			}
			fmt.Print(string(data))
		},
	}

	rootCmd.AddCommand(startCmd, stopCmd, statusCmd, urlCmd, logsCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
