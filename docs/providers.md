# Writing a host provider

Outband talks to backends through `bmc.Client`. New backends (cloud APIs, on-host agents, vendor BMCs) are **in-tree Go packages** that register a factory and are blank-imported into `cmd/outband`. There is no runtime plugin loader.

## Contract

Implement [`internal/bmc`](../internal/bmc/bmc.go):

| Piece | Required? | Notes |
|-------|-----------|--------|
| `bmc.Client` | Yes | `MCInfo`, `PowerStatus`, `PowerControl`, `Sensors`, `SEL`, `Close` |
| `bmc.Capabilities` | Yes (for correct UI) | Advertise only what works; omit the rest |
| `bmc.Console` | Optional | Serial/SOL-style byte pipe; also set `FeatureConsole` |

Rules:

- Unsupported methods return `bmc.ErrUnsupported` (or advertise fewer features and never call them).
- Busy exclusive resources (e.g. SOL) return `bmc.ErrBusy`.
- Unfinished providers may return `provider.ErrNotImplemented` from the factory so `hosts.Open` skips them with a warning (see the `unimplemented` stub in `internal/provider/stubs.go`).
- Do **not** special-case your provider in `httpapi` or the telemetry collector — capabilities and optional interfaces drive behavior.
- AMI video KVM is inventory-driven (`kvm:` block), not part of the IPMI adapter feature set. Non-IPMI hosts must not use top-level `kvm` — use `amt.kvm` or `ilo.remote_console` instead. At most one KVM backend per host.

Factory signature:

```go
provider.Register("myprovider", func(cfg config.HostConfig) (bmc.Client, error) {
    return myprovider.New(cfg)
})
```

## Package layout

1. Create `internal/myprovider/` (copy [`internal/examplehost`](../internal/examplehost/) as a starting point).
2. Register in `init()` via `provider.Register`.
3. Blank-import from [`cmd/outband/main.go`](../cmd/outband/main.go):

```go
_ "outband/internal/myprovider"
```

4. Add table-driven unit tests next to the package. Do not require a live BMC for `go test ./...`.

`examplehost` registers itself but is **not** imported by `main` — it exists as a skeleton and is exercised from its own tests.

## Inventory and options

Shared host fields: `id`, `name`, `provider`, `host`, `port`, `user`, `password`, optional `sensor_names`, optional `features`.

Shipping providers use **typed nests**:

- `ipmi:` — e.g. `cipher_suite`
- `kvm:` — AMI Adviser/IVTP (`port`, `tls`); presence enables KVM in the UI (IPMI hosts only)
- `amt:` — e.g. `tls`, nested `kvm` for Hardware-KVM (see [amt.md](amt.md), [amt-kvm.md](amt-kvm.md))
- `ilo:` — e.g. `insecure_skip_verify`, `remote_console` (see [ilo.md](ilo.md), [ilo-kvm.md](ilo-kvm.md))
- `idrac:` — e.g. `transport`, `insecure_skip_verify` (see [idrac.md](idrac.md))

Experimental or new in-tree providers should use the opaque **`options`** map keyed by provider name (JSON object per key). Decode with `cfg.ProviderOptions("myprovider")`.

When both a typed nest and `options` exist for the same concern, **typed wins** for shipping providers (IPMI ignores `options.ipmi`).

### YAML example

```yaml
- id: droplet-1
  name: DO droplet
  provider: digitalocean   # after you implement and register it
  host: api.digitalocean.com
  user: token
  password: "..."           # API token — never commit
  options:
    digitalocean:
      region: nyc3
      droplet_id: 123
```

### JSON (`OUTBAND_HOSTS`) example

```json
[
  {
    "id": "agent-1",
    "provider": "examplehost",
    "host": "127.0.0.1",
    "user": "local",
    "password": "unused",
    "options": {
      "examplehost": { "model": "lab-vm", "powered_on": true }
    }
  }
]
```

(`examplehost` is only usable if blank-imported into the binary; use it as a template, not a production provider.)

## Feature flags

Implement `Features() bmc.FeatureSet` and set only what you support:

- `FeaturePower`, `FeatureSensors`, `FeatureSEL`, `FeatureConsole`, `FeatureIdentity`
- `FeatureKVM` is driven by inventory for the selected host: top-level `kvm` (AMI), `amt.kvm`, or `ilo.remote_console` — not by inventing support in the adapter

HTTP nav/routes return 501 for missing features; the telemetry collector skips them.

### Inventory feature overrides

Per-host `features` can **disable** provider-advertised bits (does not enable missing ones):

```yaml
features:
  sensors: false   # hides Sensors + Metrics, skips sensor polls
  # sel: false
  # power: false
  # console: false
```

KVM stays inventory-driven (`kvm` / `amt.kvm` / `ilo.remote_console`); it is not controlled by `features`.

## Checklist

- [ ] Package under `internal/<name>/` implements `Client` + `Capabilities`
- [ ] Factory registered; blank-import in `cmd/outband`
- [ ] Provider-specific knobs under typed nest or `options.<name>`
- [ ] Unit tests without a live backend
- [ ] `go test ./...`
- [ ] Update README provider list if shipping a real backend
