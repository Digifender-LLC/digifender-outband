# Outband

**Browser BMC for every vendor.**

Outband is a Go + HTMX control plane for out-of-band server management. Power, sensors, SEL, serial console, and KVM — without Java applets, vendor portals, or BMC passwords in the browser.

IPMI, AMT, and iLO shipping. iDRAC next. One UI, provider-agnostic internals.

[Quick start](#quick-start) · [Features](#features) · [Providers](#providers) · [Deploy](#deploy) · [Docs](#docs)

---

## Quick start

```bash
export OUTBAND_BMC_HOST=192.168.9.74
export OUTBAND_BMC_USER=root
export OUTBAND_BMC_PASS='...'      # BMC password — never commit
export OUTBAND_UI_PASS='...'       # UI gate password — not the BMC password
export OUTBAND_LISTEN=:8080

go run ./cmd/outband
```

Open http://127.0.0.1:8080 and sign in with `OUTBAND_UI_PASS`.

**Requirements:** Go 1.25+, a reachable BMC (IPMI UDP 623, AMT TCP 16992, and/or iLO HTTPS 443; AMI KVM TCP 7578 when used).

---

## Features

| | |
|---|---|
| **Dashboard** | Identity, power state, recent health at a glance |
| **Power** | On / off / cycle / soft — confirmed actions |
| **Sensors** | Live SDR readings with optional display-name maps |
| **Metrics** | Host-keyed SQLite history, charts in the browser |
| **SEL** | System event log without a vendor applet |
| **Console** | Browser SOL via xterm.js (one session per process) |
| **KVM** | Experimental AMI Adviser/IVTP → noVNC (RFB bridge) |
| **Auth** | Local UI password and/or OIDC SSO; BMC creds stay server-side |

---

## Providers

| Provider | Status |
|----------|--------|
| `ipmi` | Shipping — IPMI 2.0 / RMCP+ |
| `amt` | Shipping — Intel AMT WS-MAN (HTTP Digest) |
| `ilo` | Shipping — HPE iLO Redfish |
| `idrac` | Stub — registered, not implemented |

Inventory can list multiple hosts; the UI binds one **active** host for now. Unimplemented providers are skipped at startup with a warning. See [docs/providers.md](docs/providers.md) to add a backend.

### Multi-host inventory

```bash
export OUTBAND_UI_PASS='...'
export OUTBAND_DEFAULT_HOST=tyan
export OUTBAND_HOSTS='[
  {
    "id": "tyan",
    "name": "Tyan BMC",
    "provider": "ipmi",
    "host": "192.168.9.74",
    "port": 623,
    "user": "root",
    "password": "'"$OUTBAND_BMC_PASS"'",
    "ipmi": { "cipher_suite": 3 },
    "kvm": { "port": 7578, "tls": false }
  }
]'

go run ./cmd/outband
```

Priority: `OUTBAND_HOSTS` (JSON) → `OUTBAND_HOSTS_FILE` (YAML/JSON) → legacy `OUTBAND_BMC_*`.

Provider options nest under `ipmi` / `kvm` / `amt` / `ilo`. IPMI hosts without a `kvm` block still enable AMI KVM by default (port 7578); do not attach AMI `kvm` to AMT or iLO hosts. Optional per-host `sensor_names` maps SDR names to UI labels (see [`hosts.example.yaml`](hosts.example.yaml)). Capabilities on the active client drive nav and telemetry.

---

## Deploy

### Docker Compose

```bash
export OUTBAND_BMC_PASS='...'
export OUTBAND_UI_PASS='...'
export OUTBAND_BMC_HOST=192.168.9.74
docker compose up --build
```

If UDP/IPMI is flaky through userland Docker networking, try `network_mode: host`. For JSON inventory, set `OUTBAND_HOSTS` / `OUTBAND_DEFAULT_HOST` instead of legacy `OUTBAND_BMC_*` — see `docker-compose.yml`.

### Prebuilt image (GHCR)

```bash
docker pull ghcr.io/theminecraftguyguru/outband:alpha
# or a release tag:
docker pull ghcr.io/theminecraftguyguru/outband:v0.1.0-alpha.1
```

### Nix

```bash
nix build && ./result/bin/outband
nix run . --                  # needs OUTBAND_* env
nix run github:TheMinecraftGuyGuru/outband/v0.1.0-alpha.2
```

Dev shell: `nix develop` (Go 1.25 + Node for CSS). Refresh `vendorHash` in `flake.nix` when Go deps change.

---

## Auth

At least one of a local UI password or complete OIDC config is required:

| Env | Role |
|-----|------|
| `OUTBAND_UI_PASS` | Shared local / break-glass password |
| `OUTBAND_OIDC_ISSUER` | IdP issuer URL |
| `OUTBAND_OIDC_CLIENT_ID` | OIDC client ID |
| `OUTBAND_OIDC_CLIENT_SECRET` | Optional for public PKCE clients |
| `OUTBAND_OIDC_REDIRECT_URL` | Exact callback, e.g. `https://outband.example/auth/oidc/callback` |

Session cookie: `outband_session` (12h). BMC credentials never reach the browser.

### Other useful env

| Env | Notes |
|-----|--------|
| `OUTBAND_LISTEN` | Default `:8080` |
| `OUTBAND_DATA_DIR` | SQLite telemetry dir (default `./data`) |
| `OUTBAND_HOSTS_FILE` | Path to hosts YAML/JSON |
| `OUTBAND_BMC_PORT` / `OUTBAND_CIPHER_SUITE` | Legacy single-host only |
| `OUTBAND_KVM_PORT` / `OUTBAND_KVM_TLS` | Legacy path; inventory uses `kvm.*` |

---

## Docs

| Doc | Topic |
|-----|--------|
| [AGENTS.md](AGENTS.md) | Contributor / agent guide |
| [docs/bmc-recon.md](docs/bmc-recon.md) | Reference BMC notes |
| [docs/amt.md](docs/amt.md) | Intel AMT |
| [docs/ilo.md](docs/ilo.md) | HPE iLO Redfish |
| [docs/kvm-protocol.md](docs/kvm-protocol.md) | AMI IVTP / Adviser wire format |
| [docs/providers.md](docs/providers.md) | Writing a host provider |

### UI CSS

Tailwind v4. Edit `internal/ui/static/css/src.css`, then:

```bash
npm install
npm run build:css    # writes app.css (committed; Go/Docker/Nix do not run Node)
```

---

## Layout

| Path | Role |
|------|------|
| `cmd/outband` | Process entrypoint |
| `internal/bmc` | `Client` + capabilities |
| `internal/provider` | Registry / factory |
| `internal/ipmi` | RMCP+ adapter |
| `internal/amt` | AMT WS-MAN adapter |
| `internal/ilo` | HPE iLO Redfish adapter |
| `internal/config` | Env, flags, inventory |
| `internal/hosts` | Live host registry |
| `internal/httpapi` | HTMX + SOL/KVM WebSockets |
| `internal/telemetry` | Host-keyed SQLite + poller |
| `internal/amiweb` / `kvm` / `rfb` | AMI login, IVTP, RFB |
| `internal/ui` | Templates + static assets |

---

## Security

- BMC username/password stay **server-side**.
- The browser authenticates with `OUTBAND_UI_PASS` and/or OIDC — never with BMC credentials.
- No in-app HTTPS yet — reverse proxy or LAN trust.
- One SOL session and one KVM session per process; second clients get a clear busy response.
- Do not commit `wireguard-export.zip`, keys, or `data/*.db*`.

---

## License

Outband is MIT-licensed — see [LICENSE](LICENSE). Third-party notices (including MIT-derived KVM/IVTP ideas from [rd450x-console](https://github.com/BadCoder1337/rd450x-console) and vendored noVNC) are in [NOTICE](NOTICE).
