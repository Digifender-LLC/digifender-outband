// DVC decoder logic adapted from the MIT-licensed pedrotei/ilo-console
// (ilo3console/dvc.py), validated against HPE iLO 4 IRC.
package rc

import "fmt"

// Decoder states (indices into the transition tables).
const (
	reset      = 0
	start      = 1
	pixels     = 2
	pixlru1    = 3
	pixlru0    = 4
	pixcode1   = 5
	pixcode2   = 6
	pixcode3   = 7
	pixgrey    = 8
	pixrgbr    = 9
	pixrpt     = 10
	pixrpt1    = 11
	pixrptstd1 = 12
	pixrptstd2 = 13
	pixrptnstd = 14
	cmd        = 15
	cmd0       = 16
	movexy0    = 17
	extcmd     = 18
	cmdx       = 19
	moveshortx = 20
	movelongx  = 21
	blkrpt     = 22
	extcmd1    = 23
	firmware   = 24
	extcmd2    = 25
	mode0      = 26
	timeout    = 27
	blkrpt1    = 28
	blkrptstd  = 29
	blkrptnstd = 30
	pixfan     = 31
	pixcode4   = 32
	pixdup     = 33
	blkdup     = 34
	pixcode    = 35
	pixspec    = 36
	exit       = 37
	latched    = 38
	movexy1    = 39
	mode1      = 40
	pixrgbg    = 41
	pixrgbb    = 42
	hunt       = 43
	print0     = 44
	print1     = 45
	corp       = 46
	mode2      = 47
)

var (
	bitsToReadInit = [48]int{
		0, 1, 1, 1, 1, 1, 2, 3, 5, 5, 1, 1, 3, 3, 8, 1, 1, 7, 1, 1, 3,
		7, 1, 1, 8, 1, 7, 0, 1, 3, 7, 1, 4, 0, 0, 0, 1, 0, 1, 7, 7, 5,
		5, 1, 8, 8, 1, 4,
	}
	next0 = [48]int{
		1, 2, 31, 2, 2, 10, 10, 10, 10, 41, 2, 33, 2, 2, 2, 16, 19, 39, 22,
		20, 1, 1, 34, 25, 46, 26, 40, 1, 29, 1, 1, 36, 10, 2, 1, 35, 8, 37,
		38, 1, 47, 42, 10, 43, 45, 45, 1, 1,
	}
	next1Init = [48]int{
		1, 15, 3, 11, 11, 10, 10, 10, 10, 41, 11, 12, 2, 2, 2, 17, 18, 39,
		23, 21, 1, 1, 28, 24, 46, 27, 40, 1, 30, 1, 1, 35, 10, 2, 1, 35, 9,
		37, 38, 1, 47, 42, 10, 0, 45, 45, 24, 1,
	}
	getMask = [9]int{0, 1, 3, 7, 15, 31, 63, 127, 255}
)

// Controller receives side-band events from the decoder.
type Controller interface {
	SetCipher(cipher int)
	SendAck()
	RefreshScreen()
	OnPower(on bool)
	OnHealth(level int)
	OnLicensed(flags int)
	OnFlags(flags int)
	OnFramerate(fps int)
	OnStatus(field int, text string)
	OnText(text string)
	OnResize(width, height int)
	OnFrame()
	OnSeize()
	OnExit()
}

// NopController implements Controller with no-ops.
type NopController struct{}

func (NopController) SetCipher(int)        {}
func (NopController) SendAck()             {}
func (NopController) RefreshScreen()       {}
func (NopController) OnPower(bool)         {}
func (NopController) OnHealth(int)         {}
func (NopController) OnLicensed(int)       {}
func (NopController) OnFlags(int)          {}
func (NopController) OnFramerate(int)      {}
func (NopController) OnStatus(int, string) {}
func (NopController) OnText(string)        {}
func (NopController) OnResize(int, int)    {}
func (NopController) OnFrame()             {}
func (NopController) OnSeize()             {}
func (NopController) OnExit()              {}

// Framebuffer stores 0x00RRGGBB pixels.
type Framebuffer struct {
	Width, Height, Generation int
	Pixels                    []uint32
}

// Resize sets a new width/height, zeroing all pixels and bumping Generation.
func (f *Framebuffer) Resize(w, h int) {
	f.Width = w
	f.Height = h
	f.Pixels = make([]uint32, w*h)
	f.Generation++
}

// Clear zeroes all pixels at the current size and bumps Generation.
func (f *Framebuffer) Clear() {
	f.Pixels = make([]uint32, f.Width*f.Height)
	f.Generation++
}

// PasteBlock copies a w-wide, h-tall block (row-major in block) at (x, y).
// Source blocks are always 16 pixels wide in the block buffer.
func (f *Framebuffer) PasteBlock(block []uint32, x, y, w, h int) {
	fbW := f.Width
	for row := 0; row < h; row++ {
		fy := y + row
		if fy < 0 || fy >= f.Height {
			continue
		}
		dst := fy*fbW + x
		src := row * 16
		for col := 0; col < w; col++ {
			fx := x + col
			if fx >= 0 && fx < fbW {
				f.Pixels[dst+col] = block[src+col]
			}
		}
	}
}

// ToRGBX serialises pixels as little-endian RGBX (R, G, B, 0) for RFB.
func (f *Framebuffer) ToRGBX() []byte {
	out := make([]byte, f.Width*f.Height*4)
	for i, px := range f.Pixels {
		off := i * 4
		out[off] = byte(px >> 16)
		out[off+1] = byte(px >> 8)
		out[off+2] = byte(px)
		out[off+3] = 0
	}
	return out
}

// Decoder is the DVC bit-stream state machine. Not thread-safe.
type Decoder struct {
	FB  *Framebuffer
	Ctl Controller

	bitsToRead [48]int
	next1      [48]int

	reversal, left, right [256]byte

	ibAcc     uint
	ibBcnt    int
	zeroCount int

	state, nextState int
	code             int
	countBytes       int

	blockWidth, blockHeight int
	bitsPerColor            int
	colorRemap              [32768]uint32

	ccActive int
	ccColor  [17]int
	ccUsage  [17]int
	ccBlock  [17]int
	pixcode  int

	block            [256]uint32
	pixelCount       int
	lastx, lasty     int
	newx, newy       int
	sizeX, sizeY     int
	yClipped         int
	screenX, screenY int
	videoDetected    bool

	red, green, blue int
	lastColor        uint32

	cmdBuf    [256]int
	cmdCount  int
	cmdLast   int
	printChan int
	printStr  string

	fatalCount   int
	timeoutCount int
}

// NewDecoder constructs a DVC decoder wired to fb and ctl.
func NewDecoder(fb *Framebuffer, ctl Controller) *Decoder {
	if ctl == nil {
		ctl = NopController{}
	}
	d := &Decoder{
		FB:            fb,
		Ctl:           ctl,
		bitsPerColor:  5,
		blockWidth:    16,
		blockHeight:   16,
		pixcode:       latched,
		videoDetected: true,
		timeoutCount:  -1,
	}
	d.bitsToRead = bitsToReadInit
	d.next1 = next1Init
	d.initReversal()
	d.buildPixelTable(5)
	return d
}

func (d *Decoder) initReversal() {
	for n := 0; n < 256; n++ {
		loMin, hi := 8, 8
		x := n
		out := 0
		for bit := 0; bit < 8; bit++ {
			out <<= 1
			if x&1 != 0 {
				if bit < loMin {
					loMin = bit
				}
				out |= 1
				hi = 7 - bit
			}
			x >>= 1
		}
		d.reversal[n] = byte(out)
		d.right[n] = byte(loMin)
		d.left[n] = byte(hi)
	}
}

// Feed processes one plaintext stream byte. Returns 0=ok, 1=exit, 4=hunt resync, 6=hang.
func (d *Decoder) Feed(b byte) int {
	return d.processBits(int(b & 0xFF))
}

func (d *Decoder) addBits(c int) int {
	d.ibAcc |= uint(c) << d.ibBcnt
	d.ibBcnt += 8
	d.zeroCount += int(d.right[c])
	if d.zeroCount > 30 {
		d.nextState = hunt
		d.state = hunt
		return 4
	}
	if c != 0 {
		d.zeroCount = int(d.left[c])
	}
	return 0
}

func (d *Decoder) getBits(n int) {
	if n == 1 {
		d.code = int(d.ibAcc & 1)
		d.ibAcc >>= 1
		d.ibBcnt--
		return
	}
	if n == 0 {
		return
	}
	val := int(d.ibAcc) & getMask[n]
	d.ibBcnt -= n
	d.ibAcc >>= n
	val = int(d.reversal[val])
	d.code = val >> (8 - n)
}

func (d *Decoder) cacheReset() {
	d.ccActive = 0
}

func (d *Decoder) pixcodeFor(active int) int {
	switch {
	case active < 2:
		return latched
	case active == 2:
		return pixlru0
	case active == 3:
		return pixcode1
	case active < 6:
		return pixcode2
	case active < 10:
		return pixcode3
	default:
		return pixcode4
	}
}

func (d *Decoder) cacheLRU(color int) int {
	active := d.ccActive
	idx := 0
	found := 0
	for i := 0; i < active; i++ {
		if color == d.ccColor[i] {
			idx = i
			found = 1
			break
		}
		if d.ccUsage[i] == active-1 {
			idx = i
		}
	}
	threshold := d.ccUsage[idx]
	if found == 0 {
		if active < 17 {
			idx = active
			threshold = active
			active++
			d.ccActive = active
			d.pixcode = d.pixcodeFor(active)
			d.next1[pixfan] = d.pixcode
		}
		d.ccColor[idx] = color
	}
	d.ccBlock[idx] = 1
	for i := 0; i < active; i++ {
		if d.ccUsage[i] < threshold {
			d.ccUsage[i]++
		}
	}
	d.ccUsage[idx] = 0
	return found
}

func (d *Decoder) cacheFind(index int) int {
	active := d.ccActive
	for i := 0; i < active; i++ {
		if index == d.ccUsage[i] {
			color := d.ccColor[i]
			for j := 0; j < active; j++ {
				if d.ccUsage[j] < index {
					d.ccUsage[j]++
				}
			}
			d.ccUsage[i] = 0
			d.ccBlock[i] = 1
			return color
		}
	}
	return -1
}

func (d *Decoder) cachePrune() {
	n := d.ccActive
	i := 0
	for i < n {
		if d.ccBlock[i] == 0 {
			n--
			d.ccBlock[i] = d.ccBlock[n]
			d.ccColor[i] = d.ccColor[n]
			d.ccUsage[i] = d.ccUsage[n]
		} else {
			d.ccBlock[i]--
			i++
		}
	}
	d.ccActive = n
	d.pixcode = d.pixcodeFor(n)
	d.next1[pixfan] = d.pixcode
}

func (d *Decoder) buildPixelTable(bpc int) {
	n2 := 1 << (bpc * 3)
	switch bpc {
	case 5:
		for v := 0; v < n2; v++ {
			d.colorRemap[v] = uint32(((v & 0x1F) << 3) | ((v & 0x3E0) << 6) | ((v & 0x7C00) << 9))
		}
	case 4:
		for v := 0; v < n2; v++ {
			d.colorRemap[v] = uint32(((v & 0xF) << 4) | ((v & 0xF0) << 8) | ((v & 0xF00) << 12))
		}
	case 3:
		for v := 0; v < n2; v++ {
			d.colorRemap[v] = uint32(((v & 0xF) << 5) | ((v & 0xF0) << 11) | ((v & 0xF00) << 15))
		}
	case 2:
		for v := 0; v < n2; v++ {
			d.colorRemap[v] = uint32(((v & 0xF) << 6) | ((v & 0xF0) << 15) | ((v & 0xF00) << 18))
		}
	}
}

func (d *Decoder) setBitsPerColor(n int) {
	d.bitsPerColor = 5 - (n & 3)
	for _, s := range []int{pixgrey, pixrgbr, pixrgbg, pixrgbb} {
		d.bitsToRead[s] = d.bitsPerColor
	}
	d.buildPixelTable(d.bitsPerColor)
}

func (d *Decoder) setHalfHeight() {
	if d.screenX > 1616 {
		if d.blockHeight != 8 {
			d.blockHeight = 8
			for _, s := range []int{movelongx, movexy0, movexy1, blkrptnstd} {
				d.bitsToRead[s] = 8
			}
		}
	} else if d.blockHeight != 16 {
		d.blockHeight = 16
		for _, s := range []int{movelongx, movexy0, movexy1, blkrptnstd} {
			d.bitsToRead[s] = 7
		}
	}
}

func (d *Decoder) nextBlock(count int) {
	paint := d.videoDetected
	if d.pixelCount != 0 && d.yClipped > 0 && d.lasty == d.sizeY {
		fill := d.colorRemap[0]
		for i := d.yClipped; i < 256; i++ {
			d.block[i] = fill
		}
	}
	d.pixelCount = 0
	d.nextState = start
	px := d.lastx * d.blockWidth
	py := d.lasty * d.blockHeight
	for count != 0 {
		if paint {
			d.FB.PasteBlock(d.block[:], px, py, 16, d.blockHeight)
		}
		px += 16
		d.lastx++
		if d.lastx >= d.sizeX {
			break
		}
		count--
	}
}

func (d *Decoder) pushPixel(value uint32) bool {
	if d.pixelCount >= d.blockHeight*d.blockWidth {
		d.nextState = latched
		return false
	}
	d.block[d.pixelCount] = value
	d.pixelCount++
	return true
}

func (d *Decoder) processBits(c int) int {
	// addBits may force HUNT when too many zero bits arrive; continue the
	// state machine on those bits (same as the MIT iLO3 reference decoder).
	_ = d.addBits(c)
	d.countBytes++
	n := 0
	for n == 0 {
		need := d.bitsToRead[d.state]
		if need > d.ibBcnt {
			return 0
		}
		d.getBits(need)
		if d.code == 0 {
			d.nextState = next0[d.state]
		} else {
			d.nextState = d.next1[d.state]
		}
		st := d.state
		hungOK := d.dispatch(st)
		if !hungOK {
			n = 6
			break
		}

		if d.nextState == pixels && d.pixelCount == d.blockHeight*d.blockWidth {
			d.nextBlock(1)
			d.cachePrune()
		}

		if d.state == d.nextState && d.state != print1 && d.state != latched && d.state != hunt {
			n = 6
			break
		}
		d.state = d.nextState
		if st == exit {
			return 1
		}
	}
	return n
}

func (d *Decoder) pixRGBB() bool {
	d.blue = d.code
	color := d.red | d.green | d.blue
	if d.cacheLRU(color) != 0 {
		d.nextState = latched
		return true
	}
	d.lastColor = d.colorRemap[color]
	d.pushPixel(d.lastColor)
	return true
}

func (d *Decoder) dispatch(st int) bool {
	switch st {
	case pixlru1, pixlru0, pixcode1, pixcode2, pixcode3, pixcode4:
		if d.ccActive == 1 {
			d.code = d.ccUsage[0]
		} else if st == pixlru0 {
			d.code = 0
		} else if st == pixlru1 {
			d.code = 1
		} else if d.code != 0 {
			d.code++
		}
		color := d.cacheFind(d.code)
		if color == -1 {
			d.nextState = latched
			return true
		}
		d.lastColor = d.colorRemap[color]
		d.pushPixel(d.lastColor)
		return true

	case pixrptstd1:
		if d.code == 7 {
			d.nextState = pixrptnstd
			return true
		}
		if d.code == 6 {
			d.nextState = pixrptstd2
			return true
		}
		d.code += 2
		for i := 0; i < d.code; i++ {
			if !d.pushPixel(d.lastColor) {
				return true
			}
		}
		return true

	case pixrptstd2:
		d.code += 8
		for i := 0; i < d.code; i++ {
			if !d.pushPixel(d.lastColor) {
				return true
			}
		}
		return true

	case pixrptnstd:
		for i := 0; i < d.code; i++ {
			if !d.pushPixel(d.lastColor) {
				return true
			}
		}
		return true

	case pixdup:
		d.pushPixel(d.lastColor)
		return true

	case pixcode:
		d.nextState = d.pixcode
		return true

	case pixrgbr:
		d.red = d.code << (d.bitsPerColor * 2)
		return true

	case pixrgbg:
		d.green = d.code << d.bitsPerColor
		return true

	case pixgrey:
		d.red = d.code << (d.bitsPerColor * 2)
		d.green = d.code << d.bitsPerColor
		return d.pixRGBB()

	case pixrgbb:
		return d.pixRGBB()

	case movexy0, mode0:
		d.newx = d.code
		if st == movexy0 && d.newx > d.sizeX {
			d.newx = 0
		}
		return true

	case movexy1:
		d.newy = d.code
		if d.blockHeight == 16 {
			d.newy &= 0x7F
		}
		d.lastx = d.newx
		d.lasty = d.newy
		return true

	case moveshortx:
		d.code = d.lastx + d.code + 1
		d.lastx = d.code
		if d.blockHeight == 16 {
			d.lastx &= 0x7F
		}
		return true

	case movelongx:
		d.lastx = d.code
		if d.blockHeight == 16 {
			d.lastx &= 0x7F
		}
		return true

	case timeout:
		if d.timeoutCount == d.countBytes-1 {
			d.nextState = latched
		}
		if d.ibBcnt&7 != 0 {
			d.getBits(d.ibBcnt & 7)
		}
		d.timeoutCount = d.countBytes
		d.Ctl.OnFrame()
		return true

	case firmware:
		if d.cmdCount != 0 {
			d.cmdBuf[d.cmdCount-1] = d.cmdLast
		}
		d.cmdCount++
		d.cmdLast = d.code
		return true

	case corp:
		if d.code != 0 {
			return true
		}
		d.dispatchCommand()
		d.cmdCount = 0
		return true

	case print0:
		d.printChan = d.code
		d.printStr = ""
		return true

	case print1:
		if d.code != 0 {
			d.printStr += string(rune(d.code))
		} else {
			switch d.printChan {
			case 1, 2:
				d.Ctl.OnStatus(2+d.printChan, d.printStr)
			case 4:
				d.Ctl.OnText(d.printStr)
			}
			d.nextState = start
		}
		return true

	case reset:
		d.cacheReset()
		d.pixelCount = 0
		d.lastx = 0
		d.lasty = 0
		d.red = 0
		d.green = 0
		d.blue = 0
		d.fatalCount = 0
		d.timeoutCount = -1
		d.cmdCount = 0
		return true

	case latched:
		if d.fatalCount == 40 || d.fatalCount == 11680 {
			d.Ctl.RefreshScreen()
		}
		d.fatalCount++
		if d.fatalCount == 120000 {
			d.Ctl.RefreshScreen()
		}
		if d.fatalCount == 12000000 {
			d.Ctl.RefreshScreen()
			d.fatalCount = 41
		}
		return true

	case blkdup:
		d.nextBlock(1)
		return true

	case blkrptstd:
		d.code += 2
		d.nextBlock(d.code)
		return true

	case blkrptnstd:
		d.nextBlock(d.code)
		return true

	case mode1:
		d.sizeX = d.newx
		d.sizeY = d.code
		return true

	case mode2:
		d.mode2()
		return true

	case hunt:
		if d.nextState != hunt {
			d.ibBcnt = 0
			d.ibAcc = 0
			d.zeroCount = 0
			d.countBytes = 0
		}
		return true

	case exit:
		d.Ctl.OnExit()
		return true
	}

	// States with no side effect: start, pixels, pixrpt, pixrpt1, blkrpt,
	// blkrpt1, pixfan, pixspec, cmd, cmd0, extcmd, cmdx, extcmd1, extcmd2.
	return true
}

func (d *Decoder) dispatchCommand() {
	op := d.cmdLast
	switch op {
	case 1:
		d.nextState = exit
	case 2:
		d.nextState = print0
	case 3:
		if d.cmdCount != 0 {
			d.Ctl.OnFramerate(d.cmdBuf[0])
		} else {
			d.Ctl.OnFramerate(0)
		}
	case 4:
		d.Ctl.OnPower(true)
	case 5:
		d.Ctl.OnPower(false)
		d.FB.Clear()
		d.newx = 50
		d.code = 38
	case 6:
		d.FB.Clear()
		d.Ctl.OnStatus(2, "")
		d.Ctl.OnFrame()
	case 7:
		// terminal-services type; not used headless
	case 9:
		if d.ibBcnt&7 != 0 {
			d.getBits(d.ibBcnt & 7)
		}
	case 10:
		d.Ctl.OnSeize()
	case 11:
		d.setBitsPerColor(d.cmdBuf[0])
	case 12:
		d.setVideoDecryption(d.cmdBuf[0])
	case 13:
		d.setBitsPerColor(d.cmdBuf[0])
		d.setVideoDecryption(d.cmdBuf[1])
		d.Ctl.OnLicensed(d.cmdBuf[2])
		d.Ctl.OnFlags(d.cmdBuf[3])
	case 16:
		d.Ctl.SendAck()
	case 128:
		d.Ctl.OnFrame()
	}
}

func (d *Decoder) setVideoDecryption(cipher int) {
	switch cipher {
	case CipherNone, CipherRC4, CipherAES128, CipherAES256:
		d.Ctl.SetCipher(cipher)
	default:
		d.Ctl.SetCipher(CipherNone)
	}
}

func (d *Decoder) mode2() {
	d.lastx = 0
	d.lasty = 0
	d.pixelCount = 0
	d.cacheReset()
	d.screenX = d.sizeX * d.blockWidth
	d.screenY = d.sizeY*16 + d.code
	d.videoDetected = d.screenX != 0 && d.screenY != 0
	if d.code > 0 {
		d.yClipped = 256 - 16*d.code
	} else {
		d.yClipped = 0
	}
	if !d.videoDetected {
		d.FB.Clear()
		d.Ctl.OnStatus(2, "no video")
		return
	}
	d.FB.Resize(d.screenX, d.screenY)
	d.setHalfHeight()
	d.Ctl.OnResize(d.screenX, d.screenY)
	d.Ctl.OnStatus(2, fmt.Sprintf("%dx%d", d.screenX, d.screenY))
}
