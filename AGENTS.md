# Outband — guide for humans and agents

Working notes for anyone (person or automated agent) changing this repository.

## What this is

Outband is a Go + HTMX front-end for BMC management (out-of-band). It exposes dashboard, power, sensors, SEL, browser SOL (xterm.js), and an experimental AMI Adviser/IVTP KVM path bridged to noVNC via RFB.

Provider internals stay vendor-agnostic (`internal/bmc` + `internal/provider`). Shipping providers: IPMI (`internal/ipmi`), Intel AMT (`internal/amt`), HPE iLO Redfish (`internal/ilo`). iDRAC is a registered stub only.

Formerly **mIPMI**; env prefix is `OUTBAND_*`, binary `outband`.

## Layout

| Path | Role |
|------|------|
| `cmd/outband` | Process entrypoint |
| `internal/bmc` | `Client` interface + feature flags |
| `internal/provider` | Provider registry / factory |
| `internal/ipmi` | RMCP+ adapter (`github.com/bougou/go-ipmi`) |
| `internal/amt` | Intel AMT WS-MAN adapter (HTTP Digest) |
| `internal/ilo` | HPE iLO Redfish adapter |
| `internal/config` | Env/flags + host inventory |
| `internal/hosts` | Live host registry (active host selection) |
| `internal/httpapi` | HTMX routes, auth gate, SOL + KVM WebSockets |
| `internal/telemetry` | Host-keyed SQLite store + background poller |
| `internal/ui` | Templates + Tailwind CSS (`src.css` → `app.css`) + vendored static assets (HTMX, xterm, noVNC) |
| `internal/amiweb` | AMI MegaRAC web login / JNLP launch args |
| `internal/kvm` | IVTP session, video decode, HID uplink, RFB bridge |
| `internal/rfb` | Minimal RFB server for noVNC |
| `docs/` | BMC recon, [AMT](docs/amt.md), [iLO](docs/ilo.md), KVM protocol, provider guide |
| `scripts/` | Ad-hoc verify/probe tools (`//go:build ignore`) |
| `flake.nix` / `flake.lock` | Nix flake (`buildGoModule`, Go 1.25) |

## Run and test

Requirements: Go 1.25+, reachable BMC (UDP 623 for IPMI; TCP 7578 for AMI KVM; TCP 16992 for AMT; HTTPS 443 for iLO).

```bash
export OUTBAND_BMC_HOST=192.168.9.74
export OUTBAND_BMC_USER=root
export OUTBAND_BMC_PASS='...'   # BMC password — never commit
export OUTBAND_UI_PASS='...'    # UI gate password / break-glass (or OIDC; see README) — never commit
export OUTBAND_LISTEN=:8080

go run ./cmd/outband
go test ./...
```

Docker: see `README.md` and `docker-compose.yml`. Prefer `OUTBAND_HOSTS` JSON inventory when running under Compose.

Nix: `nix build` / `nix run` / `nix develop` via the repo flake (`nodejs` is in the dev shell for CSS). Update `vendorHash` in `flake.nix` when Go module deps change.

UI CSS is Tailwind v4. After editing `internal/ui/static/css/src.css`, run `npm run build:css` and commit the generated `app.css` (Go/Docker builds embed that file and do not run Node).

Verification helpers under `scripts/` are not part of the main module build (`//go:build ignore`). Run them with `go run scripts/verify_bmc.go` (etc.). They read credentials from the environment (`OUTBAND_*`).

## Conventions

- **Secrets stay out of git.** No BMC passwords, UI passwords, OIDC client secrets, WireGuard private keys, or session tokens in source, docs checked in as examples, or commit messages. Use env vars (`OUTBAND_*`) or a local ignored file.
- **BMC credentials are server-side only.** The browser authenticates with `OUTBAND_UI_PASS` and/or OIDC — never with BMC credentials.
- **Match existing style.** Prefer small, focused packages; keep HTMX partials boring and readable; avoid drive-by refactors unrelated to the task.
- **Providers behind `bmc.Client`.** New vendor support goes through the registry — do not special-case iDRAC/AMT/IPMI/iLO in the HTTP layer.
- **Unimplemented inventory hosts are skipped.** Stub providers return `provider.ErrNotImplemented`; `hosts.Open` warns and continues. Unknown providers and a stub `OUTBAND_DEFAULT_HOST` still fail startup. At least one usable host is required.
- **Capabilities drive UI and polling.** Implement `bmc.Capabilities` and omit unsupported bits (`FeatureConsole`, etc.). HTTP nav/routes and the telemetry collector consult `bmc.ClientFeatures`; missing features are hidden / skipped (501 if hit directly). IPMI advertises the control plane only; AMT/iLO advertise power/sensors/SEL/identity (no console/KVM yet). **`FeatureKVM` comes from inventory `kvm` config** on the active host (AMI IVTP — do not enable on AMT/iLO hosts). SOL is via optional `bmc.Console` (advertised with `FeatureConsole`), not part of `bmc.Client`.
- **Provider-specific inventory options** nest under `ipmi` / `kvm` / `amt` / `ilo` on each host. Do not put vendor knobs on the shared top-level host fields.
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
- Real iDRAC provider (stub only); AMT/iLO console and KVM redirection
- In-process HTTPS termination (reverse proxy or LAN trust)
- Committing private network or credential material
