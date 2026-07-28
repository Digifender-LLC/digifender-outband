# HPE iLO remote console (KVM)

Outband bridges iLO’s proprietary Integrated Remote Console (IRC) into the same
noVNC/RFB UI used for AMI KVM. The iLO path is **not** RFB on the wire and does
**not** use the AMI `kvm:` inventory block.

## Target (dev)

| Item | Value |
|------|--------|
| Hardware | DL380p Gen8, iLO 4 firmware **2.82** |
| Login | `POST /json/login_session` → 16-byte hex `session_key` |
| Params | `GET /json/rc_info` → `rc_port` (often **17990**), `enc_key`, `optional_features` |
| Console | TCP to `rc_port` (plain TCP; encryption is in-protocol) |
| Client on FW | Java IRC (`/html/java_irc.html` → `intgapp4_*.jar`); no HTML5 WebSocket on this build |
| Chassis off | Decoder reports status **no video** (expected until power on) |

## Session flow

1. HTTPS login with BMC credentials (server-side only).
2. Fetch `rc_info` (cookie `sessionKey=…`).
3. TCP connect to `rc_port`; read hello `0x50` (`P`).
4. Send console request `0x2001` LE + 32-byte session token (XOR-obfuscated with
   `enc_key` **hex ASCII** when `ENCRYPT_KEY` is set; flag `0x40`/`0x80` in the
   high command byte).
5. Auth byte: `0x52` OK; `0x53`/`0x59` busy → seize (`0x0055`) or share (`0x0056`);
   Outband **seizes**.
6. Optional second TCP socket with command `0x2002` for out-of-band notifications
   (power/LED/seize); video works without it.
7. Stream starts in **DVC mode, cleartext**. A DVC header command selects RC4 /
   AES-OFB; thereafter both directions XOR with the keystream keyed by raw
   `enc_key` bytes. `ESC [ R` / `ESC [ r` also toggle encryption when not in DVC.

## Inventory

```json
{
  "id": "ilo-gen8",
  "provider": "ilo",
  "host": "192.168.9.90",
  "user": "Administrator",
  "password": "…",
  "ilo": { "insecure_skip_verify": true }
}
```

Remote console defaults **on** for `provider: "ilo"`. Disable explicitly:

```json
"ilo": { "remote_console": false }
```

Do **not** attach AMI `"kvm": {…}` to iLO hosts.

## Out of scope

- Virtual media (`vm_port` / SCSI target)
- VSP serial console
- HTML5 WebSocket IRC (not present on the probed FW)

## References

Protocol behaviour matches the Java IRC applet shipped by the BMC and the
MIT-documented iLO 3 IRC layout ([pedrotei/ilo-console](https://github.com/pedrotei/ilo-console)
`docs/PROTOCOL_ILO3.md`). Outband’s Go port is clean-room relative to AGPL
iLO 4 Node code; do not vendor AGPL sources.

## Probe / verify

```bash
export OUTBAND_BMC_HOST=192.168.9.90
export OUTBAND_BMC_USER=Administrator
export OUTBAND_BMC_PASS='…'   # never commit
go run scripts/ilo_rc_probe.go
go run scripts/verify_ilo_kvm.go
```
