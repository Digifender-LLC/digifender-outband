# AMI JViewer / Adviser KVM protocol notes

Target: Tyan S5512 BMC (`192.168.9.74`), AMI MegaRAC GoAhead, FW **S5512 R5.00** (2013), JViewer on TCP **7578** (cleartext).

Outband Path 2: native IVTP client → ASPEED/ASP-2000 decode → RFB → vendored noVNC (`/kvm`, `/ws/kvm`).

## Live path (verified 2026-07-28)

| Step | Result |
|------|--------|
| Web login | `POST /rpc/WEBSES/create.asp` → `SESSION_COOKIE` |
| JNLP | `GET /Java/jviewer.jnlp?EXTRNIP=…&JNLPSTR=JViewer` with `Cookie: SessionCookie=…` (ignore bad `Content-Length`) |
| IVTP validate | 7-byte header + MD5(session secret) → response type 35, body `1` = OK |
| Video | type 5 fragments → `ASP-2000` frames → decode (e.g. 1024×768) |
| HID | type 6 IUSB keyboard/mouse reports |

### JNLP arguments (positional)

1. BMC host  
2. Port `7578`  
3. **Session secret** (16 printable chars **plus** a trailing `0x02` on this firmware)  
4. Web `SessionCookie`

Tyan JNLP XML is corrupt: `</` in `</argument>` before the cookie is overwritten by `0x02`, so the file looks like `TOKEN\x02<argument>COOKIE`. Repair by inserting `</` after the `0x02` **without stripping it** — Adviser MD5s `TOKEN+"\x02"`. Stripping `\x02` yields validate body `0` (auth failure).

## IVTP dialect (Tyan / this JViewer.jar)

**Not** the newer MegaRAC 8-byte `uint16` opcode dialect (rd450x).

```text
HDR_SIZE = 7, little-endian
  type    uint8
  pktSize uint32   // payload bytes after header
  status  uint16
```

Selected opcodes (`IVTPPktHdr`):

| Name | Value |
|------|------:|
| VIDEO_FRAGMENT | 5 |
| HID_PKT | 6 |
| SET_BANDWIDTH | 7 |
| RESUME_REDIRECTION | 14 |
| STOP_SESSION_IMMEDIATE | 25 |
| VALIDATE_VIDEO_SESSION | 34 |
| VALIDATE_VIDEO_SESSION_RESPONSE | 35 |
| GET_KEYBD_LED | 121 |

`SESSION_TOKEN_LEN` / `HASH_SIZE` = 16. Validate body = `MD5(UTF-8 secret)` (16 bytes). Response body byte: `0` = reject, nonzero = OK.

Video fragment payload: `uint16` frag number (LE); bit `0x8000` = last fragment; low bits `0` = start of frame.

## Frame layout

```text
[0:39]    VideoHdr — signature "ASP-2000" at [19:27]
[39:125]  ASP2000ImgHdr (86 bytes; same field order as later VideoHeader)
[125:]    compressed tiles
```

Newer MegaRAC may omit the 39-byte wrapper; the decoder accepts both.

## HID

IVTP type 6 + 32-byte IUSB header + data-length + USB report (see `internal/kvm/hid.go`). Offsets are relative to the **7-byte** IVTP header.

## Attribution

IVTP/codec ideas adapted from MIT-licensed [rd450x-console](https://github.com/BadCoder1337/rd450x-console); Tyan wire format recovered from this BMC’s `JViewer.jar`.

## Related

- [bmc-recon.md](bmc-recon.md)
- UI: `/kvm` (noVNC), SOL: `/console`
