# mIPMI — guide for humans and agents

Working notes for anyone (person or automated agent) changing this repository.

## What this is

mIPMI is a Go + HTMX front-end for BMC management over IPMI 2.0 / RMCP+. It exposes dashboard, power, sensors, SEL, browser SOL (xterm.js), and an experimental AMI Adviser/IVTP KVM path bridged to noVNC via RFB.

Provider internals are meant to stay vendor-agnostic (`internal/bmc` + `internal/provider`). The shipping provider today is IPMI (`internal/ipmi`). iDRAC and AMT are registered stubs only.

## Layout

| Path | Role |
|------|------|
| `cmd/mipmi` | Process entrypoint |
| `internal/bmc` | `Client` interface + feature flags |
| `internal/provider` | Provider registry / factory |
| `internal/ipmi` | RMCP+ adapter (`github.com/bougou/go-ipmi`) |
| `internal/config` | Env/flags + host inventory |
| `internal/hosts` | Live host registry (active host selection) |
| `internal/httpapi` | HTMX routes, auth gate, SOL + KVM WebSockets |
| `internal/telemetry` | Host-keyed SQLite store + background poller |
| `internal/ui` | Templates + vendored static assets (HTMX, xterm, noVNC) |
| `internal/amiweb` | AMI MegaRAC web login / JNLP launch args |
| `internal/kvm` | IVTP session, video decode, HID uplink, RFB bridge |
| `internal/rfb` | Minimal RFB server for noVNC |
| `docs/` | BMC recon and KVM protocol notes |
| `scripts/` | Ad-hoc verify/probe tools (`//go:build ignore`) |

## Run and test

Requirements: Go 1.25+, reachable BMC on UDP 623 (and TCP 7578 for KVM on AMI Adviser).

```bash
export MIPMI_BMC_HOST=192.168.9.74
export MIPMI_BMC_USER=root
export MIPMI_BMC_PASS='...'   # BMC password — never commit
export MIPMI_UI_PASS='...'    # UI gate password — never commit
export MIPMI_LISTEN=:8080

go run ./cmd/mipmi
go test ./...
```

Docker: see `README.md` and `docker-compose.yml`. Prefer `MIPMI_HOSTS` JSON inventory when running under Compose.

Verification helpers under `scripts/` are not part of the main module build (`//go:build ignore`). Run them with `go run scripts/verify_bmc.go` (etc.). They read credentials from the environment.

## Conventions

- **Secrets stay out of git.** No BMC passwords, UI passwords, WireGuard private keys, or session tokens in source, docs checked in as examples, or commit messages. Use env vars (`MIPMI_*`) or a local ignored file.
- **BMC credentials are server-side only.** The browser only ever sees `MIPMI_UI_PASS`.
- **Match existing style.** Prefer small, focused packages; keep HTMX partials boring and readable; avoid drive-by refactors unrelated to the task.
- **Providers behind `bmc.Client`.** New vendor support goes through the registry — do not special-case iDRAC/AMT/IPMI in the HTTP layer.
- **Unimplemented inventory hosts are skipped.** Stub providers (`idrac`/`amt`) return `provider.ErrNotImplemented`; `hosts.Open` warns and continues. Unknown providers and a stub `MIPMI_DEFAULT_HOST` still fail startup. At least one usable host is required.
- **Capabilities drive UI and polling.** Implement `bmc.Capabilities` and omit unsupported bits (`FeatureConsole`, `FeatureKVM`, etc.). HTTP nav/routes and the telemetry collector consult `bmc.ClientFeatures`; missing features are hidden / skipped (501 if hit directly). IPMI advertises the full set including KVM. SOL is via optional `bmc.Console` (advertised with `FeatureConsole`), not part of `bmc.Client`.
- **Host-keyed telemetry.** Store and collector keys are host IDs; the UI still binds one active host for now.
- **One SOL session / one KVM session** per process (or per active host adapter). Second clients should get a clear busy/conflict response.
- **Tests.** Prefer table-driven unit tests next to the package (`*_test.go`). Do not require a live BMC for `go test ./...`.

## Domain cautions

- IPMI/SOL is UDP/623 with session state — flaky paths through userland Docker networking are common; host networking can help.
- AMI KVM on this project’s reference BMC is **not** standard VNC. Video is proprietary IVTP/Adviser; we decode server-side and speak RFB to noVNC. See `docs/kvm-protocol.md` before changing framing, tokens, or the codec.
- Tyan/AMI JNLP XML can be corrupt (`0x02` spliced into tags). Parsing lives in `internal/amiweb` — preserve the repair path.
- Do not commit `wireguard-export.zip`, `*.pem`, `*.key`, or `data/*.db*`.

## Change discipline

1. Read the relevant package and docs before editing protocol or codec code.
2. Keep diffs scoped to the request; leave unrelated WIP alone.
3. Update `README.md` / `docs/` when behavior users rely on changes (env vars, routes, inventory format).
4. After substantive Go changes: `go test ./...` and, when touching BMC paths you can reach, the matching `scripts/verify_*.go`.
5. Never force-push shared history or rewrite published commits unless explicitly asked.

## Out of scope (for now)

- Multi-BMC fleet UI (inventory exists; UI is still single active host)
- Real iDRAC / Intel AMT providers (stubs only)
- In-process HTTPS termination (reverse proxy or LAN trust)
- Committing private network or credential material
