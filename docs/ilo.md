# HPE iLO (Redfish)

Outband talks to HPE iLO over **HTTPS Redfish** with HTTP Basic auth (not IPMI/RMCP+).

## Target notes (dev)

| Item | Value |
|------|--------|
| Endpoint | `https://HOST:443/redfish/v1/` (trailing `/` required on iLO 4) |
| Auth | Basic; typical local user `Administrator` |
| Inventory | `"provider": "ilo"`; default port `443` |
| Probed | iLO 4 firmware 2.82 on DL380p Gen8 — Redfish `1.0.0` |

Do **not** attach AMI `kvm` inventory to iLO hosts — that enables the MegaRAC IVTP bridge, which is unrelated. Graphical KVM uses the iLO IRC path (`ilo.remote_console`); see [ilo-kvm.md](ilo-kvm.md).

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

Optional nest (TLS verify skip defaults to **true**; remote console defaults to **true** when omitted):

```json
"ilo": { "insecure_skip_verify": true, "remote_console": true }
```

Set `"remote_console": false` to hide KVM for that host. Set `"insecure_skip_verify": false` only when the iLO presents a cert you trust via the system CA store.

## Capabilities

| Feature | Source |
|---------|--------|
| Identity | ServiceRoot + `Managers/1` + `Systems/1` |
| Power status | `Systems/1` → `PowerState` |
| Power control | `POST …/Systems/1/Actions/ComputerSystem.Reset/` |
| Sensors | `Chassis/1/Thermal/` Temperatures/Fans; on failure, synthetic System Health + Power Consumed |
| SEL | `Managers/1/LogServices/IEL/Entries/` (`Items[]`) |
| KVM | Inventory `ilo.remote_console` (default on) → IRC/DVC → RFB/noVNC ([ilo-kvm.md](ilo-kvm.md)) |

Power action mapping: `on`→`On`, `off`→`ForceOff`, `cycle`→`ForceRestart`, `soft`→`PushPowerButton`.

Virtual Serial (VSP) is **not** implemented yet. Virtual media is out of scope for the KVM bridge.

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
go run scripts/verify_ilo_kvm.go   # needs network to the iLO
```
