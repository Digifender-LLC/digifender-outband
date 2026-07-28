package codec

import (
	"encoding/binary"
	"fmt"
)

// Tyan / older AMI ASP-2000 frame layout (JViewer FrameHdr.set):
//
//	[0:39]   VideoHdr wrapper (signature "ASP-2000" at [19:27])
//	[39:125] ASP2000ImgHdr (86 bytes) — same field order as newer VideoHeader
//	[125:]   compressed tile payload
//
// Newer MegaRAC (rd450x) omits the 39-byte VideoHdr and starts at the 86-byte
// header; we accept both.

const (
	videoHdrSize      = 39
	asp2000HeaderSize = 86
	videoHeaderSize   = asp2000HeaderSize // alias used by comments / tests
)

// frameHeader carries the fields the decoder consumes, named after
// VideoEngineInfo.FRAME_HEADER / SourceModeInfo / DestinationModeInfo.
type frameHeader struct {
	sourceX, sourceY int
	destX, destY     int

	compressionMode      int
	jpegScaleFactor      int
	jpegTableSelector    int
	jpegYUVTableMapping  int
	sharpModeSelection   int
	advanceTableSelector int
	advanceScaleFactor   int
	numberOfMB           int
	rc4Enable            int
	rc4Reset             int
	mode420              int

	compressSize int
}

// parseFrameHeader ports FrameHdr.set() / ASP2000ImgHdr.setASPVariables().
func parseFrameHeader(frame []byte) (frameHeader, []byte, error) {
	b := frame
	if len(b) >= videoHdrSize+8 && string(b[19:27]) == "ASP-2000" {
		b = b[videoHdrSize:]
	}
	if len(b) < asp2000HeaderSize {
		return frameHeader{}, nil, fmt.Errorf("kvm/codec: frame too short: %d < %d", len(b), asp2000HeaderSize)
	}
	le := binary.LittleEndian

	var h frameHeader
	h.sourceX = int(le.Uint16(b[4:]))
	h.sourceY = int(le.Uint16(b[6:]))
	h.destX = int(le.Uint16(b[13:]))
	h.destY = int(le.Uint16(b[15:]))
	h.compressionMode = int(b[42])
	h.jpegScaleFactor = int(b[43])
	h.jpegTableSelector = int(b[44])
	h.jpegYUVTableMapping = int(b[45])
	h.sharpModeSelection = int(b[46])
	h.advanceTableSelector = int(b[47])
	h.advanceScaleFactor = int(b[48])
	h.numberOfMB = int(int32(le.Uint32(b[49:])))
	h.rc4Enable = int(b[53])
	h.rc4Reset = int(b[54])
	h.mode420 = int(b[55])
	h.compressSize = int(int32(le.Uint32(b[69:])))

	return h, b[asp2000HeaderSize:], nil
}
