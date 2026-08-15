# Quickflare-go

`quickflare-go` is a lightweight CLI tool to manage Cloudflare Tunnels (Quick Tunnels or preconfigured tunnels) as a background daemon with built-in keep-alive and auto-restart capabilities.

## Installation

You can quickly download the latest binary for your architecture directly from the GitHub releases and make it globally available:

### Linux (AMD64)

```bash
sudo curl -L -o /usr/local/bin/quickflare https://github.com/tna76874/quickflare-go/releases/download/latest/quickflare-go-linux-amd64
sudo chmod +x /usr/local/bin/quickflare

```

### Linux (ARM64 / Raspberry Pi)

```bash
sudo curl -L -o /usr/local/bin/quickflare https://github.com/tna76874/quickflare-go/releases/download/latest/quickflare-go-linux-arm64
sudo chmod +x /usr/local/bin/quickflare

```

---

## Usage

### Start as a Daemon

To start your local service running on a specific port in the background:

```bash
quickflare start --host 127.0.0.1 --port 8080 --daemon

```

### Check Status & URL

```bash
quickflare status
quickflare url

```

### View Logs

```bash
quickflare logs

```

### Stop the Tunnel

```bash
quickflare stop

```
