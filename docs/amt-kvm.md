# Intel AMT Hardware-KVM

Outband bridges AMT Hardware-KVM to the same noVNC UI as AMI KVM (`/h/{id}/kvm`).
The wire path is **not** AMI IVTP — do not attach a top-level `kvm:` nest to AMT hosts.

## Live target (dev)

| Item | Value |
|------|--------|
| Inventory id | `amt` |
| Host | `192.168.8.45` |
| WS-MAN | TCP **16992** (HTTP Digest); HTTPS **16993** when `amt.tls` |
| Redirection | TCP **16994** (clear); **16995** (TLS) |
| User | `admin` (MEBx password via inventory / `OUTBAND_BMC_*`) |
| Creds | env / local `hosts.yaml` only — never commit |

Port **5900** (legacy VNC password RFB) is obsolete on modern ME firmware and is not used.

## Inventory

```yaml
- id: amt
  name: Intel AMT
  provider: amt
  host: 192.168.8.45
  port: 16992
  user: admin
  password: "…"
  amt:
    kvm: {}          # enables FeatureKVM; port defaults 16994
    # kvm: { port: 16995, tls: true }
```

Presence of `amt.kvm` enables KVM. Top-level AMI `kvm:` on an AMT host is wrong (would wire the MegaRAC IVTP bridge).

## Enable sequence (WS-MAN)

Before dialing redirection, Outband (mirroring MeshCommander):

1. `AMT_RedirectionService.RequestStateChange` with `EnabledState = 32768 | (IDER?1:0) | (SOL?2:0)`
2. `CIM_KVMRedirectionSAP.RequestStateChange(2)` — enable KVM SAP (3 = disable)
3. `PUT AMT_RedirectionService` with `ListenerEnabled=true`

Classes: `IPS_KVMRedirectionSettingData` (pixel / opt-in settings), `CIM_KVMRedirectionSAP`, `AMT_RedirectionService`.

If `CIM_KVMRedirectionSAP` is missing, the SKU has no Hardware-KVM — do not advertise FeatureKVM.

## Redirection handshake

Reference: MeshCommander / MeshCentral `amt-redir-*.js` (Apache-2.0; reimplemented in Go under `internal/amt/redir`).

```text
TCP connect → 16994/16995
Client:  StartRedirectionSession  0x10 0x01 0x00 0x00 'K''V''M''R'
Server:  StartRedirectionSessionReply 0x11 (status 0 = OK; 2 = busy)
Client:  AuthenticateSession query     0x13 … authType=0
Server:  auth types (Digest=4)
Client:  empty Digest request (user + /RedirectionService)
Server:  challenge (realm, nonce, qop)
Client:  Digest response (MD5)
Server:  status 0 success
Client:  0x40 … (KVM open)
Server:  0x41 … then RFB stream on the same socket
```

## RFB dialect

After `0x41`, the stream is RFB 3.8. Security type **None** (already authenticated). AMT encodings used by Outband:

| Encoding | ID | Notes |
|----------|-----|--------|
| RAW | 0 | RGB565 (2 bpp) default → converted to RGBX for noVNC |
| RLE | 16 | AMT LRE tiles; zlib stored or zlib-wrapped (see below) |
| DesktopSize | -223 (`0xFFFFFF21`) | resize |

RLE tiles are zlib-framed LRE (MeshCommander `_decodeLRE`). Most tiles use an
uncompressed stored block (`0x00 ‖ len ‖ ~len ‖ payload`); the first tile of a
session is often a full zlib member starting `78 9c`. Outband must inflate that
before LRE — treating `0x78` as a subencoding aborts with a black screen.
At 4K, Outband negotiates **RGB332** so the ME framebuffer budget is not exceeded.

Input: standard RFB `KeyEvent` / `PointerEvent` (X11 keysyms; AMT accepts a LATIN1/MISCELLANY subset).

## Architecture in Outband

```text
noVNC ──WS── /h/{id}/ws/kvm ── rfb.Serve (server)
                                 ▲
                          rfb.Source / Sink
                                 ▲
                    internal/amt/redir.Bridge
                         │         │
              EnableKVM (WS-MAN)   Dial KVMR + RFB client
```

Session busy handling matches AMI: one active session per host → HTTP 409.

## Verify

```bash
export OUTBAND_BMC_HOST=192.168.8.45
export OUTBAND_BMC_USER=admin
export OUTBAND_BMC_PASS='...'
go run scripts/probe_amt_kvm.go   # status + enable + dial
go run scripts/verify_amt_kvm.go  # enable → one frame → exit 0
```

## Recon status

Live against `192.168.8.45` (AMT 12.0.95.2489, SKU 16392):

- WS-MAN **16992** and redirection **16994** reachable; TLS ports closed
- `CIM_KVMRedirectionSAP.SystemName` is **`ManagedSystem`** (not `Intel(r) AMT`)
- Opt-in was on (`OptInPolicy` / `OptInRequired=1`); EnableKVM clears it when `CanModifyOptInPolicy` allows
- Desktop is **3840×2160**; RGB565 full-frame exceeds the ME buffer — Outband negotiates **RGB332** + **RLE (16)**
- RLE payloads are zlib-framed LRE; first tile often starts `78 9c` (must inflate — raw `0x78` is not a subencoding)
- `scripts/verify_amt_kvm.go` receives a full frame successfully
