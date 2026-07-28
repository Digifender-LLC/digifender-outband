# Intel AMT (WS-MAN)

Outband talks to Intel AMT over **HTTP Digest WS-MAN** (not IPMI/RMCP+).

## Target notes (dev)

| Item | Value |
|------|--------|
| Endpoint | `http://HOST:16992/wsman` (HTTPS `:16993` when `amt.tls` is set) |
| Auth | Digest, user usually `admin`, password = MEBx password |
| Inventory | `"provider": "amt"`; default port `16992` |
| KVM | Hardware-KVM via redirection **16994/16995** — see [amt-kvm.md](amt-kvm.md) |

Do **not** attach AMI top-level `kvm` inventory to AMT hosts — that enables the MegaRAC IVTP bridge, which is unrelated. Use `amt.kvm` instead.

## Inventory example

```json
{
  "id": "amt1",
  "name": "Workstation AMT",
  "provider": "amt",
  "host": "192.168.8.45",
  "user": "admin",
  "password": "…"
}
```

Optional nest:

```json
"amt": { "tls": true, "kvm": {} }
```

`amt.kvm` enables Hardware-KVM (FeatureKVM) over the redirection listener.

## Verify

```bash
export OUTBAND_BMC_HOST=192.168.8.45
export OUTBAND_BMC_USER=admin
export OUTBAND_BMC_PASS='...'
# optional: OUTBAND_AMT_TLS=1  OUTBAND_BMC_PORT=16993
go run scripts/verify_amt.go
go run scripts/verify_amt_kvm.go   # when amt.kvm / Hardware-KVM available
```

## Capabilities

AMT advertises power / sensors / SEL / identity. **KVM** is inventory-gated via `amt.kvm` (not advertised by the WS-MAN adapter itself). SOL over AMT redirection is not in Outband yet.
