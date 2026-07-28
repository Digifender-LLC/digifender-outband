# Dell iDRAC (multi-generation)

Outband talks to Dell iDRAC over HTTPS. **Each inventory host picks its own wire protocol** so one deployment can mix generations (e.g. iDRAC7 web + iDRAC9 Redfish).

## Transports

| Transport | Typical hardware | Auth | Notes |
|-----------|------------------|------|--------|
| `redfish` | iDRAC8/9/10 (and late iDRAC7 with Redfish) | SessionService → `X-Auth-Token` | Preferred when `/redfish/v1` responds |
| `web` | iDRAC6/7 classic UI | `POST data/login` + session cookie (+ `ST2`) | Used when Redfish is absent |
| `wsman` | iDRAC7+ OPENWSMAN | HTTP Basic | DCIM_* CIM classes |
| `auto` (default) | any | — | Probe once per host: Redfish → web → WS-MAN |

TLS: the client tries modern TLS (1.2+) first, then **TLS 1.0 + RSA ciphers** for older BMCs (iDRAC7). Self-signed / expired certs are skipped by default.

Do **not** attach AMI `kvm` inventory to iDRAC hosts.

## Inventory examples

Mixed fleet (auto-detect per host):

```json
[
  {
    "id": "r620",
    "name": "PowerEdge R620",
    "provider": "idrac",
    "host": "192.168.9.237",
    "user": "root",
    "password": "…"
  },
  {
    "id": "r740",
    "provider": "idrac",
    "host": "192.168.9.50",
    "user": "root",
    "password": "…",
    "idrac": { "transport": "redfish" }
  }
]
```

Force classic web on iDRAC7:

```json
"idrac": { "transport": "web", "insecure_skip_verify": true }
```

`insecure_skip_verify` defaults to **true** when omitted.

## Capabilities (v1)

| Feature | Redfish | Web (iDRAC7) | WS-MAN |
|---------|---------|--------------|--------|
| Identity | ServiceRoot + Managers/Systems | `data?get=sysDesc,fwVersion,…` | `DCIM_SystemView` |
| Power status | `Systems/1` PowerState | `pwState` | `DCIM_ComputerSystem` |
| Power control | `ComputerSystem.Reset` | `data?set=pwState:N` | `RequestStateChange` |
| Sensors | Chassis Thermal (+ synthetic) | synthetic from web scalars | `DCIM_NumericSensor` |
| SEL | Systems LogServices/SEL | empty (API TBD) | `DCIM_SELLogEntry` |

Web power map: `on`→1, `off`→0, `cycle`→2, `soft`→5 (graceful).  
Redfish power map: On / ForceOff / ForceRestart / GracefulShutdown.

Console and HTML5 KVM are **not** implemented yet.

## Lab notes (192.168.9.237)

| Item | Value |
|------|--------|
| Platform | PowerEdge R620 / **iDRAC7** (`idrac-3QFPZV1`) |
| TLS | TLS 1.0 only (modern clients need legacy cipher suite) |
| Redfish | **404** |
| WS-MAN | `/wsman` → `WWW-Authenticate: Basic realm="OPENWSMAN"` |
| Web | `login.html` → `POST data/login` |
| IPMI :623 | closed on this host |

## Verify

```bash
export OUTBAND_BMC_HOST=192.168.9.237
export OUTBAND_BMC_USER=root
export OUTBAND_BMC_PASS='…'   # never commit
# optional: OUTBAND_IDRAC_TRANSPORT=web|wsman|redfish|auto
go run scripts/verify_idrac.go
```

Optional: `OUTBAND_BMC_PORT`, `OUTBAND_IDRAC_INSECURE=0` to enforce TLS verify.
