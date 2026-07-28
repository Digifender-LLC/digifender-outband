# BMC recon — Tyan S5512 / AMI MegaRAC

Target used during mIPMI development.

| Item | Value |
|------|-------|
| Host | `192.168.9.74` |
| Vendor | Tyan S5512 (IANA manufacturer ID 6653), AMI MegaRAC / GoAhead |
| Firmware | S5512 R5.00 (2013-09-16) |
| Creds (dev) | Factory AMI default is often `root` / device-specific — **env only; never commit** |
| IPMI | UDP 623, IPMI 2.0 / RMCP+ |
| SOL | Enabled @ 38.4 kbps (payload on UDP 623) |
| Web UI | HTTP 80 (legacy TLS on 443) |
| KVM | AMI JViewer TCP **7578** — proprietary Adviser/IVTP; mIPMI bridges decoded video to noVNC. See [kvm-protocol.md](kvm-protocol.md). |

## Access paths

- **Primary:** IPMI 2.0 / RMCP+ from the mIPMI Go process (`github.com/bougou/go-ipmi`).
- **Telemetry:** process-lifetime collector → SQLite (`MIPMI_DATA_DIR`); HTTP reads the store.
- **Dev network:** WireGuard tunnel `sorrel-mIPMI` from the laptop to the LAN.
- **Prod target:** Docker/LXC container on the LAN; set `MIPMI_BMC_HOST` (or `MIPMI_HOSTS`) to the BMC LAN IP.

## KVM notes

- Web session: `POST /rpc/WEBSES/create.asp` → `SESSION_COOKIE`.
- JNLP: `/Java/jviewer.jnlp` (args: host, `7578`, opaque token, session cookie).
- JAR confirms Adviser validate/encryption/video-fragment path — not noVNC.
- Full spike write-up: [kvm-protocol.md](kvm-protocol.md).

## Out of scope (current)

- Multi-BMC **fleet UI** (host picker / per-host routes) — inventory + host-keyed internals exist; UI still binds one active host
- Real iDRAC / Intel AMT providers (stubs registered only)
- HTTPS termination inside mIPMI (use reverse proxy or LAN trust)
- KMCrypt / encrypted HID and some Adviser control opcodes (see KVM TODOs in `internal/kvm`)
