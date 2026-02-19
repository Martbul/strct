# Agent
Focus: Linux & Hardware Control.
Runs on: Orange Pi 3B (ARM64).

internal/
├── agent/          # lifecycle orchestration only
├── api/            # HTTP server plumbing (cors, server)
├── config/         # config loading + device ID
│
├── features/       # ← domain logic lives here
│   ├── cloud/
│   ├── adblocker/
│   ├── monitor/
│   ├── router/
│   └── vpn/
│
├── platform/       # ← OS/hardware abstractions (NEW grouping)
│   ├── disk/       # move from internal/disk
│   ├── wifi/       # move from internal/wifi
│   └── tunnel/     # move from internal/network/tunnel
│
├── setup/          # captive portal (one-time init flow)
├── ota/            # self-update
│
└── httputil/       # ← shared HTTP helpers (answers Q2)
    └── respond.go