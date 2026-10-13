# Outband — guide for humans and agents

Working notes for anyone (person or automated agent) changing this repository.

## What this is

Outband is a Go + HTMX front-end for BMC management (out-of-band). It exposes dashboard, power, sensors, SEL, browser SOL (xterm.js), and KVM bridged to noVNC via RFB (AMI Adviser/IVTP, Intel AMT Hardware-KVM, and HPE iLO IRC).

Provider internals stay vendor-agnostic (`internal/bmc` + `internal/provider`). Shipping providers: IPMI (`internal/ipmi`), Intel AMT (`internal/amt`), HPE iLO Redfish (`internal/ilo`), Dell iDRAC (`internal/idrac` — Redfish/web/WS-MAN auto per host).

Env prefix is `OUTBAND_*`, binary `outband`.

## Layout

| Path | Role |
|------|------|
| `cmd/outband` | Process entrypoint |
| `internal/bmc` | `Client` interface + feature flags |
| `internal/provider` | Provider registry / factory |
| `internal/ipmi` | RMCP+ adapter (`github.com/bougou/go-ipmi`) |
| `internal/amt` | Intel AMT WS-MAN adapter (HTTP Digest) + `amt/redir` Hardware-KVM |
| `internal/ilo` | HPE iLO Redfish adapter |
| `internal/ilo/rc` | iLO IRC remote console (DVC → RFB) |
| `internal/idrac` | Dell iDRAC multi-gen (Redfish / web / WS-MAN) |
| `internal/config` | Env/flags + host inventory |
| `internal/hosts` | Live host registry |
| `internal/httpapi` | HTMX routes, auth gate, SOL + KVM WebSockets |
| `internal/telemetry` | Host-keyed SQLite store + background poller |
| `internal/ui` | Templates + Tailwind CSS (`src.css` → `app.css`) + vendored static assets (HTMX, xterm, noVNC) |
| `internal/amiweb` | AMI MegaRAC web login / JNLP launch args |
| `internal/kvm` | IVTP session, video decode, HID uplink, RFB bridge |
| `internal/rfb` | Minimal RFB server for noVNC |
| `docs/` | BMC recon, [hardware matrix](docs/hardware-matrix.md), [AMT](docs/amt.md), [AMT KVM](docs/amt-kvm.md), [iLO](docs/ilo.md), [iLO KVM](docs/ilo-kvm.md), [iDRAC](docs/idrac.md), KVM protocol, provider guide |
| `scripts/` | Ad-hoc verify/probe tools (`//go:build ignore`) |
| `flake.nix` / `flake.lock` | Nix flake (`buildGoModule`, Go 1.25) |

## Run and test

Requirements: Go 1.25+, reachable BMC (UDP 623 for IPMI; TCP 7578 for AMI KVM; TCP 16992/16994 for AMT; HTTPS 443 for iLO/iDRAC).

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
- **Capabilities drive UI and polling.** Implement `bmc.Capabilities` and omit unsupported bits (`FeatureConsole`, etc.). HTTP nav/routes and the telemetry collector consult `Host.Features()` (client capabilities + inventory KVM + optional `features:` disables); missing features are hidden / skipped (501 if hit directly). IPMI advertises the control plane only; AMT/iLO/iDRAC advertise power/sensors/SEL/identity (no serial console yet). **`FeatureKVM` comes from inventory**: top-level `kvm` = AMI IVTP (IPMI hosts); `amt.kvm` = AMT Hardware-KVM redirection; `ilo.remote_console` (default on for `provider: "ilo"`) = iLO IRC. Do not attach AMI `kvm` to AMT/iLO/iDRAC hosts. Per-host inventory may set `features.sensors: false` (also `sel` / `power` / `console`) to hide menus and skip polls when a platform does not expose that data. SOL is via optional `bmc.Console` (advertised with `FeatureConsole`), not part of `bmc.Client`.
- **Provider-specific inventory options** nest under `ipmi` / `kvm` / `amt` / `ilo` / `idrac` on each host. Do not put vendor knobs on the shared top-level host fields. AMT KVM options nest under `amt.kvm` (not top-level AMI `kvm`).
- **Host-keyed telemetry.** Store and collectors key by host ID; one background collector runs per usable inventory host. The UI selects a host via `/h/{id}/…` (topbar picker).
- **One SOL session / one KVM session** per host adapter (KVM bridge). Second clients should get a clear busy/conflict response.
- **Tests.** Prefer table-driven unit tests next to the package (`*_test.go`). Do not require a live BMC for `go test ./...`.

## Domain cautions

- IPMI/SOL is UDP/623 with session state — flaky paths through userland Docker networking are common; host networking can help.
- AMI KVM on this project’s reference BMC is **not** standard VNC. Video is proprietary IVTP/Adviser; we decode server-side and speak RFB to noVNC. See `docs/kvm-protocol.md` before changing framing, tokens, or the codec.
- AMT KVM uses the redirection listener (16994/16995) + Digest + RFB. See `docs/amt-kvm.md` before changing handshake or encodings.
- Tyan/AMI JNLP XML can be corrupt (`0x02` spliced into tags). Parsing lives in `internal/amiweb` — preserve the repair path.
- Do not commit `wireguard-export.zip`, `*.pem`, `*.key`, or `data/*.db*`.

## Change discipline

1. Read the relevant package and docs before editing protocol or codec code.
2. Keep diffs scoped to the request; leave unrelated WIP alone.
3. Update `README.md` / `docs/` when behavior users rely on changes (env vars, routes, inventory format).
4. After substantive Go changes: `go test ./...` and, when touching BMC paths you can reach, the matching `scripts/verify_*.go`.
5. Never force-push shared history or rewrite published commits unless explicitly asked.

## Out of scope (for now)

- Multi-BMC **fleet overview** (all-hosts dashboard) — per-host UI routes (`/h/{id}/…`) and host picker exist; no aggregate fleet page yet
- iDRAC console / KVM; AMT SOL; iLO VSP serial
- In-process HTTPS termination (reverse proxy or LAN trust)
- Committing private network or credential material

## Cursor Cloud specific instructions

- **No real BMC is reachable in the cloud VM.** Standard commands live in the `## Run and test` section above; the notes here only cover what is non-obvious when there is no live hardware.
- **The app still starts and serves the UI without a BMC** — IPMI connections are lazy (`internal/ipmi`), so a placeholder inventory host is enough to exercise login and host-scoped navigation. Example run:
  ```bash
  export OUTBAND_UI_PASS=devpass
  export OUTBAND_DEFAULT_HOST=lab
  export OUTBAND_HOSTS='[{"id":"lab","name":"Lab BMC","provider":"ipmi","host":"127.0.0.1","port":623,"user":"root","password":"x"}]'
  go run ./cmd/outband   # open http://127.0.0.1:8080, log in with OUTBAND_UI_PASS
  ```
- **Expected noise:** the per-host telemetry collector logs continuous `WARN msg="poll sensors/power" ... connection refused` because the placeholder BMC is unreachable. This is normal in the cloud and is **not** a startup/build failure — dashboard/power/sensors pages render with "No … data yet" / "Waiting for collector…" placeholders.
- **Lint = `go vet ./...`** (there is no golangci-lint config or lint CI; the only workflow, `.github/workflows/container.yml`, just builds the release image on tags).
- **`go test ./...` needs no live BMC** and passes offline (already stated in conventions).
- **CSS:** `npm run build:css` regenerates `internal/ui/static/css/app.css` deterministically; only rebuild/commit it when you edit `src.css` (Go/Docker builds embed the committed file and do not run Node).
