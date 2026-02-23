# strct-agent

A lightweight Linux agent for the Orange Pi 3B that acts as a programmable home router. Manages WiFi AP/extender modes, ad blocking, VPN subnet routing, bandwidth monitoring, and local cloud storage — all exposed over a local HTTP API.

## Overview

```
Internet → eth0 → Orange Pi 3B (strct-agent) → wlan0 AP → connected devices
```

The agent runs as a single binary, requires no runtime dependencies, and is designed to be deployed once and forgotten.

## Requirements

- Go 1.23+
- Target: Linux ARM64 (Orange Pi 3B / Raspberry Pi)
- Root access on the target device (iptables, nmcli, hostapd, dnsmasq)

## Quick Start

```sh
# Run locally in dev mode (no sudo, mocked hardware)
make dev

# Run on real hardware
make run

# Cross-compile for ARM64
make build-arm64

# Deploy to device
make install DEVICE=pi@192.168.1.10
```

## Project Layout

```
cmd/agent/          # Entry point — wires services together
internal/
├── agent/          # Lifecycle orchestration (start, shutdown, health)
├── api/            # HTTP server (CORS, graceful shutdown)
├── config/         # Config loading, device ID persistence
├── errs/           # Structured error types with HTTP mapping
├── features/
│   ├── adblocker/  # StevenBlack blocklist → dnsmasq address= directives
│   ├── cloud/      # Local file storage over HTTP
│   ├── monitor/    # Latency/bandwidth metrics, backend reporting
│   ├── router/     # hostapd, iptables, tc, per-device block/limit
│   ├── vpn/        # Tailscale subnet routing and exit node
│   └── wifi/       # AP and extender mode (hostapd + dnsmasq + NAT)
├── httputil/       # Consistent JSON response helpers
├── humanize/       # Human-readable byte sizes
├── logger/         # slog initialisation (text in dev, JSON in prod)
├── netx/           # Outbound IP detection
├── platform/
│   ├── disk/       # SSD detection, mounting, size queries
│   ├── executil/   # os/exec abstraction (Real, Mock, DevRunner)
│   ├── tunnel/     # frpc reverse proxy lifecycle
│   └── wifi/       # nmcli wrapper (RealWiFi, MockWiFi)
└── setup/          # One-time captive portal for WiFi provisioning
ota/                # Self-update via signed binary swap
e2e/                # End-to-end tests (build tag: e2e)
```

## Configuration

Configuration is loaded from environment variables (or a `.env` file at the repo root). All values have defaults for local development.

| Variable               | Default              | Description                        |
|------------------------|----------------------|------------------------------------|
| `VPS_IP`               | `127.0.0.1`          | frps server address                |
| `VPS_PORT`             | `7000`               | frps server port                   |
| `AUTH_TOKEN`           | `default-secret`     | Shared token for frp tunnel auth   |
| `DOMAIN`               | `localhost`          | Agent subdomain on the VPS         |
| `BACKEND_URL`          | `https://dev.api.strct.org` | Backend API base URL        |
| `PPROF_PORT`           | `6060`               | pprof HTTP port (localhost only)   |
| `TAILSCALE_CLIENT_ID`  | _(empty)_            | Tailscale OAuth client ID          |
| `TAILSCALE_AUTH_TOKEN` | _(empty)_            | Tailscale pre-auth key             |

The binary also accepts two build-time variables injected via `-ldflags`:

```sh
go build -ldflags "-X main.DefaultDomain=strct.org -X main.DefaultVPSIP=1.2.3.4" ./cmd/agent
```

## Development

### Running locally

```sh
make dev        # mock WiFi, disk, and hardware commands — no root needed
make run        # real hardware, requires sudo
```

Dev mode stubs all hardware commands (`iptables`, `hostapd`, `nmcli`, `tc`, `tailscale`, …) and returns realistic fake output so parsers work normally. See `internal/platform/executil/dev.go`.

### Testing

```sh
make test              # unit tests with race detector
make test-cover        # unit tests + HTML coverage report
make test-pkg PKG=./internal/features/cloud   # single package
make test-e2e          # build real binary, run e2e suite
```

Tests use table-driven style and interface injection — no real hardware or root access needed. Mocks live in `executil.Mock`; the `DevRunner` handles dev-mode command stubbing in the running binary.

### Linting

```sh
make lint      # gofmt check + go vet + staticcheck
make fmt       # format in place
```

### Profiling

```sh
make pprof-vm                       # SSH tunnel from dev VM → localhost:6060
make pprof-opi                      # SSH tunnel from Orange Pi
make pprof-cpu                      # 30s CPU flame graph (tunnel must be open)
make pprof-heap                     # heap profile
make pprof-goroutines               # goroutine dump (leak detection)
```

## HTTP API

All endpoints are served on port `8080` (redirected from the configured port in dev mode).

| Method | Path                        | Description                         |
|--------|-----------------------------|-------------------------------------|
| GET    | `/api/health`               | Agent health + internet status      |
| GET    | `/api/status`               | Disk usage, uptime, IP              |
| GET    | `/api/files`                | List files (`?path=/subdir`)        |
| POST   | `/api/mkdir`                | Create directory                    |
| DELETE | `/api/delete`               | Delete file or directory            |
| POST   | `/strct_agent/fs/upload`    | Upload file (multipart, 50 GB max)  |
| GET    | `/api/network/stats`        | Latency, loss, bandwidth            |
| POST   | `/api/network/speedtest`    | Trigger speed test                  |
| GET    | `/api/wifi/config`          | Current WiFi config                 |
| POST   | `/api/wifi/config`          | Set WiFi mode (router/extender/off) |
| GET    | `/api/wifi/status`          | Active mode, subnet, connected IPs  |
| GET    | `/api/wifi/scan`            | Scan visible networks               |
| POST   | `/api/wifi/stop`            | Disable WiFi AP                     |
| GET    | `/api/router/config`        | Router settings                     |
| POST   | `/api/router/config`        | Update router settings              |
| GET    | `/api/router/devices`       | Connected devices (MAC, IP, status) |
| POST   | `/api/router/block`         | Block/unblock device by MAC         |
| POST   | `/api/router/limit`         | Bandwidth-limit device (tc htb)     |
| GET    | `/api/vpn/config`           | Tailscale config                    |
| POST   | `/api/vpn/config`           | Enable/disable VPN subnet routing   |
| GET    | `/api/vpn/status`           | Tailscale connection status         |
| POST   | `/api/vpn/stop`             | Disconnect Tailscale                |
| GET    | `/api/adblock/config`       | Ad blocker config                   |
| POST   | `/api/adblock/config`       | Enable/disable ad blocking          |
| GET    | `/api/adblock/status`       | Blocked domain count, last update   |
| POST   | `/api/adblock/update`       | Force blocklist refresh             |

## Deployment

### First-time setup

On first boot without internet, the agent starts a captive WiFi portal (`Strct-Setup-XXXX`) that lets you select a network and enter credentials from any browser. Once connected, the portal shuts itself down and normal services start.

### Release build

```sh
make build-arm64 DEFAULT_DOMAIN=strct.org DEFAULT_VPS_IP=1.2.3.4
make install DEVICE=pi@192.168.1.10
make install-service DEVICE=pi@192.168.1.10
```

Releases are also built automatically by GitHub Actions on any `v*` tag push and attached as a GitHub Release artifact (`strct-agent-arm64`).

### systemd

```sh
sudo systemctl status strct-agent
sudo journalctl -u strct-agent -f
```

## Architecture Notes

**Service lifecycle** — each feature is a `Service` (single `Start(ctx) error` method). The agent starts them all concurrently in goroutines and waits for `SIGINT`/`SIGTERM` to cancel the shared context, which cascades shutdown to every service.

**Hardware abstraction** — all `os/exec` calls go through `executil.Runner`. Production code injects `executil.Real{}`. Tests inject `*executil.Mock`. Dev mode injects `DevRunner`, which stubs hardware commands and returns realistic fake output so parsers exercise real code paths.

**No global state** — services communicate through narrow interfaces, not shared globals. `vpn` reads wifi state via a `wifiStatusReader` interface; `adblock` reads it the same way. Neither imports the other's concrete type.

**Error handling** — errors are wrapped with `fmt.Errorf("op: %w", err)` at every boundary. The `errs` package adds structured context (op, kind, user-facing message) and maps to HTTP status codes. Panics are never used outside of template parsing at startup.

## License

MIT
