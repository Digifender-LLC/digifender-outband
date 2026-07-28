# Hardware compatibility matrix

Living record of BMC / management-controller platforms exercised against Outband — by maintainers and community reports. Use it to see what is known to work, which inventory knobs matter, and where code or docs still need work for a given host family.

**Do not put passwords, session cookies, WireGuard keys, or other secrets in this file.** Prefer public model + firmware strings over private lab IPs (lab IPs may stay in per-vendor recon docs).

## Status legend

| Status | Meaning |
|--------|---------|
| `works` | Feature used successfully on that platform |
| `partial` | Works with limits, workarounds, or empty data |
| `fail` | Tried; broken or unsupported on this firmware |
| `untested` | Not exercised (or not applicable) |
| `n/a` | Provider / inventory does not offer this feature |

Reporter: `maintainer` (repo authors) or a GitHub handle / “community”.

Outband version: git tag or short SHA when known (e.g. `v0.1.0-alpha.3`).

## Summary

| Platform | Provider | Firmware / BMC | Identity | Power | Sensors | SEL | Console | KVM | Status | Outband | Reporter |
|----------|----------|----------------|----------|-------|---------|-----|---------|-----|--------|---------|----------|
| Tyan S5512 (AMI MegaRAC) | `ipmi` | S5512 R5.00 (2013) | works | works | works | works | works | works (AMI IVTP :7578) | reference | ≥ alpha.2 | maintainer |
| Intel AMT | `amt` | 12.0.95.2489 (SKU 16392) | works | works | partial | partial | n/a | works (`amt.kvm`) | works | ≥ alpha.3 | maintainer |
| HPE DL380p Gen8 / iLO 4 | `ilo` | iLO 4 **2.82** (Redfish 1.0.0) | works | works | works | works | n/a | works (IRC) | works | ≥ alpha.3 | maintainer |
| Dell PowerEdge R620 / iDRAC7 | `idrac` | iDRAC7 (web; Redfish 404) | works | works | partial | fail (web) | n/a | n/a | partial | ≥ alpha.3 | maintainer |

“Status” is the overall usability call for that row (not a single feature).

## How to add a report

1. Copy the [entry template](#entry-template) below (or add a summary row + short notes).
2. Fill what you actually tested. Mark unknowns `untested` — do not guess.
3. Call out **inventory** quirks (`features:`, `amt.kvm`, `ilo.remote_console`, `idrac.transport`, …) and **code / doc follow-ups** (links to issues or “needs change in `internal/…`”).
4. Open a PR against this file. Small, focused reports are welcome.

Optional: attach redacted `scripts/verify_*.go` output or a short note of Outband log lines (no credentials).

---

## Maintainer lab (seed)

Deep wire notes live in the linked docs; this section is the matrix view only.

### Tyan S5512 — AMI MegaRAC / IPMI + AMI KVM

| Field | Value |
|-------|--------|
| Provider | `ipmi` (+ top-level `kvm`, default port 7578) |
| Board / BMC | Tyan S5512, AMI MegaRAC GoAhead |
| Firmware | S5512 R5.00 (2013-09-16) |
| Outband | reference platform since early alphas |
| Reporter | maintainer |

| Feature | Result | Notes |
|---------|--------|--------|
| Identity / power / sensors / SEL | works | RMCP+ via `github.com/bougou/go-ipmi` |
| SOL console | works | UDP 623; flaky through userland Docker NAT — host networking helps |
| KVM | works | Proprietary Adviser/IVTP → RFB; JNLP may splice `0x02` into XML |

**Code / quirks:** IVTP dialect and JNLP repair are Tyan-specific; see [kvm-protocol.md](kvm-protocol.md), [bmc-recon.md](bmc-recon.md). Other AMI MegaRAC generations may need codec or framing changes — report them as new rows.

### Intel AMT — WS-MAN + Hardware-KVM

| Field | Value |
|-------|--------|
| Provider | `amt` with `amt.kvm: {}` |
| Firmware | 12.0.95.2489, SKU 16392 (Hardware-KVM present) |
| Outband | ≥ `v0.1.0-alpha.3` |
| Reporter | maintainer |

| Feature | Result | Notes |
|---------|--------|--------|
| Identity / power | works | HTTP Digest WS-MAN |
| Sensors / SEL | partial | Often empty classes; use inventory `features.sensors` / `features.sel: false` when hollow |
| KVM | works | Redirection :16994 + RFB; `EnableKVM` must succeed or connect fails clearly |

**Code / quirks:** Do not attach AMI `kvm:` to AMT hosts. Missing `CIM_KVMRedirectionSAP` ⇒ no Hardware-KVM on that SKU. See [amt.md](amt.md), [amt-kvm.md](amt-kvm.md).

### HPE DL380p Gen8 — iLO 4 Redfish + IRC

| Field | Value |
|-------|--------|
| Provider | `ilo` (`ilo.remote_console` default on) |
| Firmware | iLO 4 **2.82**, Redfish `1.0.0` |
| Outband | ≥ `v0.1.0-alpha.3` |
| Reporter | maintainer |

| Feature | Result | Notes |
|---------|--------|--------|
| Identity / power / sensors / SEL | works | Redfish Basic; trailing `/` on Redfish root required on iLO 4 |
| KVM | works | IRC / DVC → RFB; may seize an existing console session |

**Code / quirks:** Do not attach AMI `kvm:`. iLO 5/6 and other FW trains are **untested** here — please report. See [ilo.md](ilo.md), [ilo-kvm.md](ilo-kvm.md).

### Dell PowerEdge R620 — iDRAC7 (web)

| Field | Value |
|-------|--------|
| Provider | `idrac` (`auto` → web; Redfish absent) |
| Firmware | iDRAC7 (`idrac-3QFPZV1` style) |
| Outband | ≥ `v0.1.0-alpha.3` |
| Reporter | maintainer |

| Feature | Result | Notes |
|---------|--------|--------|
| Identity / power | works | Classic web login; TLS 1.0 + RSA fallback |
| Sensors | partial | Synthetic scalars from web |
| SEL | fail / omitted | Web SEL API TBD; FeatureSEL not advertised on web transport |
| Console / KVM | n/a | Not implemented for iDRAC yet |

**Code / quirks:** Prefer `idrac.transport: web` when Redfish 404s. WS-MAN path exists but is less exercised on this box. iDRAC8/9/10 Redfish rows wanted. See [idrac.md](idrac.md).

---

## Community reports

_None yet — first report lands here._

<!-- Example:
### Vendor Model — BMC name

| Field | Value |
|-------|--------|
| Provider | `…` |
| Firmware | … |
| Outband | v0.1.0-alpha.3 |
| Reporter | @github-handle |
| Date | 2026-… |

| Feature | Result | Notes |
|---------|--------|--------|
| … | … | … |

**Inventory:** …
**Code / follow-ups:** …
-->

---

## Entry template

Copy into **Community reports** (or add a summary-table row + link).

```markdown
### <Vendor> <Model> — <BMC / management controller>

| Field | Value |
|-------|--------|
| Provider | `ipmi` \| `amt` \| `ilo` \| `idrac` |
| Firmware | <BMC / FW string> |
| Outband | <tag or short SHA> |
| Reporter | <@handle or community> |
| Date | YYYY-MM-DD |

| Feature | Result | Notes |
|---------|--------|--------|
| Identity | works \| partial \| fail \| untested | |
| Power | | |
| Sensors | | |
| SEL | | |
| Console | works \| n/a \| … | |
| KVM | works \| n/a \| … | backend: AMI / AMT / iLO / none |

**Inventory:** (nests, `features:`, ports, TLS)

**Code / follow-ups:** (what broke; suspected package; issue link)
```
