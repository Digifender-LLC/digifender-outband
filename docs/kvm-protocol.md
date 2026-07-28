# AMI JViewer / Adviser KVM protocol notes

Target: Tyan S5512 BMC (`192.168.9.74`), AMI MegaRAC GoAhead web UI, JViewer on TCP **7578**.

## Capture summary (2026-07-28)

| Step | Result |
|------|--------|
| Web login | `POST /rpc/WEBSES/create.asp` with `WEBVAR_USERNAME` / `WEBVAR_PASSWORD` returns `SESSION_COOKIE` (AMI web session token). |
| JNLP | `GET http://BMC/Java/jviewer.jnlp` with `Cookie: SessionCookie=<token>` (BMC `Content-Length` is wrong — use `--ignore-content-length`). |
| JAR | `http://BMC/Java/release/JViewer.jar` (~218 KiB), package `com.ami.kvm.jviewer.*`. |
| TCP 7578 | Port open; **no banner**. Server waits for a framed client packet. Raw / length-prefixed token writes time out with no reply. |

### JNLP arguments

Parsed from live JNLP (XML is slightly corrupt: a `0x02` byte splices argument tags):

1. BMC host (e.g. `192.168.9.74`)
2. Port `7578`
3. Short opaque token (~16 chars) — appears per-launch / per-session
4. Web `SessionCookie` value (same string returned by `WEBSES/create.asp`)

Native libs: `Linux_x86_64.jar` / Win / Mac under `Java/release/` (HID helpers).

## Protocol class (from JViewer.jar strings / class names)

This is **not** RFB, VNC, WebSocket, or HTML5 KVM. It is AMI’s proprietary **Adviser / IVTP** stack:

- Framing: `com.ami.kvm.jviewer.kvmpkts.IVTPPktHdr` (`HDR_SIZE`, `type`, `pktSize`, `SESSION_TOKEN_LEN`, `HASH_SIZE`)
- Session: `ADVISER_VALIDATE_VIDEO_SESSION` → `ADVISER_VALIDATE_VIDEO_SESSION_RESPONSE` (`VALID_SESSION` / `INVALID_VIDEO_SESSION_TOKEN` / `ERR_MAX_SESSION`)
- Challenge path: `ADVISER_GET_CHALLENGE`, hashed session token (`Failed to create hashed session token`)
- Encryption: `ADVISER_ENABLE_ENCRYPTION` / `ADVISER_ENCRYPTION_KEY` / `ADVISER_INITIAL_ENCRYPTION_STATUS`; video `FrameHdr_RC4Enable` / `FrameHdr_RC4Reset`; `Decoder$rc4_state`; HID `KMCrypt`
- Video: `ADVISER_VIDEO_FRAGMENT`, `ASP2000ImgHdr`, Huffman JPEG (`JPEG_*`, `Huffman_table`), YUV422/YUV444 → RGB (`Decoder$YUV*`)
- Input: `ADVISER_HID_PKT`, USB/PS2 keyboard & mouse report classes

Observed command-name constants (non-exhaustive):  
`ADVISER_LOGIN`, `ADVISER_VIDEO_FRAGMENT`, `ADVISER_HID_PKT`, `ADVISER_REFRESH_VIDEO_SCREEN`, `ADVISER_SET_COMPRESSION_TYPE`, `ADVISER_SET_FPS`, `ADVISER_SET_BANDWIDTH`, `ADVISER_BLANK_SCREEN`, `ADVISER_PAUSE_REDIRECTION`, `ADVISER_STOP_SESSION_IMMEDIATE`, AST2K video-engine get/set, color-gain / mouse-cursor helpers.

## Packet shapes (known / open)

### Known at high level

```text
Client --TCP/7578--> Adviser
  IVTPPktHdr { type, pktSize, ... } + payload
  type ≈ ADVISER_VALIDATE_VIDEO_SESSION (session cookie / short token / digest)
Server -->
  ADVISER_VALIDATE_VIDEO_SESSION_RESPONSE
  optional ADVISER_INITIAL_ENCRYPTION_STATUS / ENCRYPTION_KEY
  stream of ADVISER_VIDEO_FRAGMENT (tile/JPEG/YUV, optionally RC4)
Client -->
  ADVISER_HID_PKT (keyboard/mouse; may be KMCrypt-wrapped)
```

Exact binary layout of `IVTPPktHdr` (endianness, field widths, digest algorithm) was **not** recovered without a live Java session + tcpdump or full bytecode decompilation. Integer pool around the class includes sizes such as `7`, `8`, `16`, `32` (consistent with small fixed header + token/hash lengths).

### Open questions

1. Exact `IVTPPktHdr` wire layout and command opcode numbering.
2. How the 16-char JNLP token relates to the web session cookie / challenge response.
3. RC4 key derivation (from encryption-key packet vs session material).
4. Tile/fragment payload layout after `ASP2000ImgHdr` / `FrameHdr` (macroblock map, skip codes).
5. Whether SSL wrapping is ever used on this firmware (`sslEncryption` string exists; this unit answered clear TCP on 7578 with no banner).

## Implementation in this repo

mIPMI speaks IVTP on the server and exposes RFB to the browser:

| Piece | Location |
|-------|----------|
| AMI web login + JNLP args | `internal/amiweb` |
| IVTP session, codec, HID | `internal/kvm` (+ `internal/kvm/codec`) |
| RFB for noVNC | `internal/rfb` |
| HTTP `/kvm` + `/ws/kvm` | `internal/httpapi` |
| Browser viewer | vendored noVNC under `internal/ui/static/novnc` |

Still evolving: KMCrypt-wrapped HID, some Adviser control messages, and edge cases around encrypted video frames. SOL on `/console` remains the more mature remote console path.

## Related

- [bmc-recon.md](bmc-recon.md) — BMC inventory
- UI: `/kvm` (noVNC), `/console` (SOL)
