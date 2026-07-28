# HPE iLO (Redfish)

Outband talks to HPE iLO over **HTTPS Redfish** with HTTP Basic auth (not IPMI/RMCP+).

## Target notes (dev)

| Item | Value |
|------|--------|
| Endpoint | `https://HOST:443/redfish/v1/` (trailing `/` required on iLO 4) |
| Auth | Basic; typical local user `Administrator` |
| Inventory | `"provider": "ilo"`; default port `443` |
| Probed | iLO 4 firmware 2.82 on DL380p Gen8 — Redfish `1.0.0` |

Do **not** attach AMI `kvm` inventory to iLO hosts — that enables the MegaRAC IVTP bridge, which is unrelated. Virtual Serial / HTML5 KVM are not implemented yet.

## Inventory example

```json
{
  "id": "ilo-gen8",
  "name": "DL380p iLO",
  "provider": "ilo",
  "host": "192.168.9.90",
  "user": "Administrator",
  "password": "…"
}
```

Optional nest (TLS verify skip defaults to **true** when omitted):

```json
"ilo": { "insecure_skip_verify": true }
```

Set `"insecure_skip_verify": false` only when the iLO presents a cert you trust via the system CA store.

## Capabilities (v1)

| Feature | Source |
|---------|--------|
| Identity | ServiceRoot + `Managers/1` + `Systems/1` |
| Power status | `Systems/1` → `PowerState` |
| Power control | `POST …/Systems/1/Actions/ComputerSystem.Reset/` |
| Sensors | `Chassis/1/Thermal/` Temperatures/Fans; on failure, synthetic System Health + Power Consumed |
| SEL | `Managers/1/LogServices/IEL/Entries/` (`Items[]`) |

Power action mapping: `on`→`On`, `off`→`ForceOff`, `cycle`→`ForceRestart`, `soft`→`PushPowerButton`.

Console (VSP) and iLO remote console/KVM are **not** implemented yet.

## iLO 4 quirks

- Always request paths with a trailing slash (`/Systems/1/` not `/Systems/1`).
- Prefer collection `Items` over `Members` for IEL (Members may be `@odata.id` stubs only).
- Self-signed HTTPS is common; insecure skip-verify is the default.

## Verify

```bash
export OUTBAND_BMC_HOST=192.168.9.90
export OUTBAND_BMC_USER=Administrator
export OUTBAND_BMC_PASS='…'   # never commit
go run scripts/verify_ilo.go
```
