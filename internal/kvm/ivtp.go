package kvm

import (
	"encoding/binary"
	"io"
)

// Tyan / older AMI MegaRAC Adviser IVTP (JViewer ~2013):
//
//	HDR_SIZE = 7, little-endian:
//	  type    uint8
//	  pktSize uint32  — payload bytes following the header
//	  status  uint16
//
// Newer MegaRAC (rd450x) uses an 8-byte header with uint16 opcodes; this
// client targets the 7-byte dialect used by the S5512 BMC on TCP 7578.
const HeaderSize = 7

// Opcodes from com.ami.kvm.jviewer.kvmpkts.IVTPPktHdr (Tyan jar).
const (
	opVideoFragment     uint8 = 5
	opHIDPkt            uint8 = 6
	opSetBandwidth      uint8 = 7
	opSetFPS            uint8 = 10
	opRefreshVideo      uint8 = 12
	opPauseRedirection  uint8 = 13
	opResumeRedirection uint8 = 14
	opBlankScreen       uint8 = 15
	opStopSession       uint8 = 25
	opEnableEncryption  uint8 = 26
	opDisableEncryption uint8 = 27
	opEncryptionKey     uint8 = 28
	opEncryptionStatus  uint8 = 29
	opInitialEncryption uint8 = 30
	opValidateVideo     uint8 = 34
	opValidateVideoResp uint8 = 35
	opGetKeybdLED       uint8 = 121
)

const sessionTokenLen = 16 // SESSION_TOKEN_LEN / HASH_SIZE (MD5)

// header is a 7-byte IVTP packet header.
type header struct {
	Type   uint8
	Size   uint32
	Status uint16
}

func (h header) marshal() []byte {
	b := make([]byte, HeaderSize)
	b[0] = h.Type
	binary.LittleEndian.PutUint32(b[1:5], h.Size)
	binary.LittleEndian.PutUint16(b[5:7], h.Status)
	return b
}

func readHeader(r io.Reader) (header, error) {
	var b [HeaderSize]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return header{}, err
	}
	return header{
		Type:   b[0],
		Size:   binary.LittleEndian.Uint32(b[1:5]),
		Status: binary.LittleEndian.Uint16(b[5:7]),
	}, nil
}

// putFixed writes s into dst as a fixed-width, zero-padded ASCII field.
func putFixed(dst []byte, s string) {
	n := copy(dst, s)
	for i := n; i < len(dst); i++ {
		dst[i] = 0
	}
}
