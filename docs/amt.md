# Intel AMT (WS-MAN)

Outband talks to Intel AMT over **HTTP Digest WS-MAN** (not IPMI/RMCP+).

## Target notes (dev)

| Item | Value |
|------|--------|
| Endpoint | `http://HOST:16992/wsman` (HTTPS `:16993` when `amt.tls` is set) |
| Auth | Digest, user usually `admin`, password = MEBx password |
| Inventory | `"provider": "amt"`; default port `16992` |

Do **not** attach AMI `kvm` inventory to AMT hosts — that enables the MegaRAC IVTP bridge, which is unrelated.

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
"amt": { "tls": true }
```

## Verify

```bash
export OUTBAND_BMC_HOST=192.168.8.45
export OUTBAND_BMC_USER=admin
export OUTBAND_BMC_PASS='...'
# optional: OUTBAND_AMT_TLS=1  OUTBAND_BMC_PORT=16993
go run scripts/verify_amt.go
```

## Capabilities

AMT advertises the control-plane features it implements. SOL/KVM over AMT are not in Outband yet — omit those features rather than advertising and failing at runtime.
