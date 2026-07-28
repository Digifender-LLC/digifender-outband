// Package rc implements HPE iLO Integrated Remote Console (IRC) over TCP,
// decoding proprietary DVC video and bridging frames to RFB for noVNC.
//
// Session/crypto/input layout follows the MIT-documented iLO 3 IRC protocol
// (pedrotei/ilo-console), validated against iLO 4 v2.82 on live hardware.
package rc

import (
	"crypto/aes"
	"errors"
	"fmt"
)

// ErrBusy is returned when another remote-console session is already active.
var ErrBusy = errors.New("iLO remote console session already active")

// Cipher selectors from the DVC set_encryption / header commands.
const (
	CipherNone   = 0
	CipherRC4    = 1
	CipherAES128 = 2
	CipherAES256 = 3
)

// Keystream is a bidirectional stream-cipher PRGA (one byte at a time).
type Keystream interface {
	RandomValue() byte
	// UpdateKey re-runs the key schedule (RC4 key-change). No-op for AES-OFB.
	UpdateKey()
}

// MakeKeystream builds a keystream for cipher, keyed with raw enc_key bytes.
func MakeKeystream(cipher int, key []byte) (Keystream, error) {
	switch cipher {
	case CipherNone:
		return nil, nil
	case CipherRC4:
		if len(key) < 16 {
			return nil, fmt.Errorf("rc4: key too short")
		}
		return NewRC4(key[:16]), nil
	case CipherAES128:
		return NewAESOFB(key, 16)
	case CipherAES256:
		return NewAESOFB(key, 32)
	default:
		return nil, fmt.Errorf("unknown cipher %d", cipher)
	}
}

// RC4 is standard RC4 PRGA with a 16-byte key (firmware style).
type RC4 struct {
	pre  [16]byte
	sbox [256]byte
	i, j int
}

// NewRC4 returns an RC4 keystream keyed with the first 16 bytes of key.
func NewRC4(key []byte) *RC4 {
	r := &RC4{}
	copy(r.pre[:], key[:16])
	r.UpdateKey()
	return r
}

// UpdateKey re-runs the RC4 KSA from the original key.
func (r *RC4) UpdateKey() {
	var sbox [256]byte
	for n := 0; n < 256; n++ {
		sbox[n] = byte(n)
	}
	j := 0
	for n := 0; n < 256; n++ {
		j = (j + int(sbox[n]) + int(r.pre[n%16])) & 0xff
		sbox[n], sbox[j] = sbox[j], sbox[n]
	}
	r.sbox = sbox
	r.i, r.j = 0, 0
}

// RandomValue advances RC4 and returns the next keystream byte.
func (r *RC4) RandomValue() byte {
	r.i = (r.i + 1) & 0xff
	r.j = (r.j + int(r.sbox[r.i])) & 0xff
	r.sbox[r.i], r.sbox[r.j] = r.sbox[r.j], r.sbox[r.i]
	return r.sbox[(int(r.sbox[r.i])+int(r.sbox[r.j]))&0xff]
}

// AESOFB is AES in OFB mode with an all-zero IV (firmware keystream).
type AESOFB struct {
	block    cipherBlock
	feedback [16]byte
	pos      int
}

type cipherBlock interface {
	Encrypt(dst, src []byte)
}

// NewAESOFB builds an AES-OFB keystream. keyLen is 16 or 32.
func NewAESOFB(key []byte, keyLen int) (*AESOFB, error) {
	if len(key) < keyLen {
		return nil, fmt.Errorf("aes: need %d key bytes", keyLen)
	}
	b, err := aes.NewCipher(key[:keyLen])
	if err != nil {
		return nil, err
	}
	return &AESOFB{block: b}, nil
}

// UpdateKey is a no-op for AES-OFB.
func (a *AESOFB) UpdateKey() {}

// RandomValue returns the next AES-OFB keystream byte.
func (a *AESOFB) RandomValue() byte {
	if a.pos == 0 {
		a.block.Encrypt(a.feedback[:], a.feedback[:])
	}
	b := a.feedback[a.pos]
	a.pos = (a.pos + 1) & 0x0f
	return b
}

// XORSessionKey obfuscates the 32-char session token for the handshake when
// ENCRYPT_KEY is set. encKeyHex is the ASCII hex string from rc_info (not raw bytes).
func XORSessionKey(token, encKeyHex string, obfuscate bool) []byte {
	raw := []byte(token)
	if !obfuscate || encKeyHex == "" {
		return raw
	}
	enc := []byte(encKeyHex)
	for i := range raw {
		raw[i] ^= enc[i%len(enc)]
	}
	return raw
}
