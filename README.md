# mIPMI

Go + HTMX BMC UI for IPMI 2.0 / RMCP+ (provider-agnostic internals). Dashboard, power, sensors, SEL, browser **SOL** (xterm.js), and experimental AMI Adviser/IVTP **KVM** bridged to noVNC.

See [docs/bmc-recon.md](docs/bmc-recon.md) for the development BMC notes, [docs/kvm-protocol.md](docs/kvm-protocol.md) for the AMI KVM wire format, and [AGENTS.md](AGENTS.md) for contributor guidance.

## Requirements

- Go 1.25+
- Reachable BMC on UDP 623 (WireGuard or LAN); TCP 7578 for AMI KVM
- Env credentials (never commit passwords)

## Run locally

### Legacy single-host (still supported)

```bash
export MIPMI_BMC_HOST=192.168.9.74
export MIPMI_BMC_USER=root
export MIPMI_BMC_PASS='...'      # BMC password
export MIPMI_UI_PASS='...'       # UI gate password (not the BMC password)
export MIPMI_LISTEN=:8080

go run ./cmd/mipmi
```

### Multi-host inventory via env (Compose-friendly)

```bash
export MIPMI_UI_PASS='...'
export MIPMI_DEFAULT_HOST=tyan
export MIPMI_HOSTS='[
  {
    "id": "tyan",
    "name": "Tyan BMC",
    "provider": "ipmi",
    "host": "192.168.9.74",
    "port": 623,
    "user": "root",
    "password": "'"$MIPMI_BMC_PASS"'"
  }
]'

go run ./cmd/mipmi
```

Inventory priority: `MIPMI_HOSTS` (JSON) → `MIPMI_HOSTS_FILE` (YAML/JSON path) → legacy `MIPMI_BMC_*`.

The UI still binds to one **active** host (`MIPMI_DEFAULT_HOST`, or the first inventory entry). Fleet UI is not implemented yet; internals and telemetry are host-keyed for a later multi-host UI.

Providers: `ipmi` (implemented); `idrac` / `amt` are registered stubs (not implemented). Unimplemented inventory entries are skipped at startup with a warning; the process fails if no usable hosts remain or if `MIPMI_DEFAULT_HOST` points at a stub. The active default must be an implemented provider.

UI nav and telemetry follow `bmc.Capabilities` on the active host’s client. The IPMI provider advertises the full control plane plus KVM. Providers must omit unsupported features rather than advertising them and failing at runtime.

Open http://127.0.0.1:8080 and log in with `MIPMI_UI_PASS`.

Optional:

- `MIPMI_BMC_PORT` (default `623`) — legacy path only
- `MIPMI_CIPHER_SUITE` (default library choice) — legacy path; inventory uses `cipher_suite` per host
- `MIPMI_HOSTS_FILE` — path to a YAML or JSON hosts file

## Docker

```bash
export MIPMI_BMC_PASS='...'
export MIPMI_UI_PASS='...'
export MIPMI_BMC_HOST=192.168.9.74
docker compose up --build
```

On a LAN host next to the BMC, point `MIPMI_BMC_HOST` at the BMC LAN IP. If UDP/IPMI is flaky through userland Docker networking, try `network_mode: host` in compose.

To use JSON inventory in Compose, set `MIPMI_HOSTS` (and optionally `MIPMI_DEFAULT_HOST`) instead of the legacy `MIPMI_BMC_*` vars — see comments in `docker-compose.yml`.

### Prebuilt image (GHCR)

Alpha images are published to GitHub Container Registry on version tags:

```bash
docker pull ghcr.io/theminecraftguyguru/mipmi:alpha
# or a specific release:
docker pull ghcr.io/theminecraftguyguru/mipmi:v0.1.0-alpha.1
```

Run with the same env vars as Compose (`MIPMI_UI_PASS`, BMC credentials or `MIPMI_HOSTS`, etc.). Package pages: [ghcr.io/theminecraftguyguru/mipmi](https://github.com/TheMinecraftGuyGuru/mipmi/pkgs/container/mipmi).

## Layout

- `cmd/mipmi` — process entrypoint
- `internal/bmc` — `Client` interface + capabilities
- `internal/provider` — provider registry/factory (`ipmi`, stub `idrac`/`amt`)
- `internal/hosts` — host registry
- `internal/ipmi` — RMCP+ adapter (`github.com/bougou/go-ipmi`)
- `internal/config` — process + host inventory
- `internal/httpapi` — HTMX routes + WebSocket SOL/KVM bridges
- `internal/telemetry` — host-keyed SQLite store + collector
- `internal/amiweb` / `internal/kvm` / `internal/rfb` — AMI web login, IVTP KVM, RFB for noVNC
- `internal/ui` — templates + vendored HTMX/xterm/noVNC static assets
- `AGENTS.md` — guide for humans and agents working in this tree

## Security notes

- BMC username/password stay **server-side** only.
- The browser only sees the UI password gate (`MIPMI_UI_PASS`).
- No HTTPS in-app for v1 — put a reverse proxy in front or trust the LAN.
- One SOL session per active host adapter; a second client is rejected until the first disconnects.

## WireGuard

Do not commit `wireguard-export.zip` or private keys. Keep tunnel config outside the repo.

## License

mIPMI is MIT-licensed — see [LICENSE](LICENSE). Third-party notices (including MIT-derived KVM/IVTP ideas from [rd450x-console](https://github.com/BadCoder1337/rd450x-console) and vendored noVNC) are in [NOTICE](NOTICE).
