package rc

import "testing"

func TestXORSessionKey(t *testing.T) {
	token := "aabbccddeeff00112233445566778899"
	enc := "0123456789abcdef0123456789abcdef"
	got := XORSessionKey(token, enc, true)
	wantHex := "51505051575652535d5c07045354545702030100000103020e0f56555b5c5c5f"
	if hexEncode(got) != wantHex {
		t.Fatalf("xor = %s, want %s", hexEncode(got), wantHex)
	}
	plain := XORSessionKey(token, enc, false)
	if string(plain) != token {
		t.Fatalf("no-obfuscate mutated token")
	}
}

func TestRC4KnownAnswer(t *testing.T) {
	key := mustHex("0123456789abcdef0123456789abcdef")
	r := NewRC4(key)
	var out [16]byte
	for i := range out {
		out[i] = r.RandomValue()
	}
	want := "7494c2e7104b08790d4bd553328f1efc"
	if hexEncode(out[:]) != want {
		t.Fatalf("rc4 = %s, want %s", hexEncode(out[:]), want)
	}
}

func TestAES128OFBKnownAnswer(t *testing.T) {
	key := mustHex("0123456789abcdef0123456789abcdef")
	a, err := NewAESOFB(key, 16)
	if err != nil {
		t.Fatal(err)
	}
	var out [16]byte
	for i := range out {
		out[i] = a.RandomValue()
	}
	want := "79abc5c23868ad84d388ce61110a6274"
	if hexEncode(out[:]) != want {
		t.Fatalf("aes128 = %s, want %s", hexEncode(out[:]), want)
	}
}

func TestKeyboardMouseFrames(t *testing.T) {
	k := KeyboardReport(0x05, []byte{0x4c})
	if len(k) != 10 || k[0] != 0x01 || k[2] != 0x05 || k[4] != 0x4c {
		t.Fatalf("keyboard frame %#v", k)
	}
	m := MouseAbsolute(100, 50, 200, 100, 0, 0, mouseLeft)
	if len(m) != 10 || m[0] != 0x02 {
		t.Fatalf("mouse frame %#v", m)
	}
	// 3000 * 100/200 = 1500 = 0x05dc
	if m[2] != 0xdc || m[3] != 0x05 {
		t.Fatalf("mouse x bytes %#v", m[2:4])
	}
	if RFBButtonsToILO(0x01) != mouseLeft || RFBButtonsToILO(0x04) != mouseRight {
		t.Fatal("button map")
	}
}

func hexEncode(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0xf]
	}
	return string(out)
}

func mustHex(s string) []byte {
	b := make([]byte, len(s)/2)
	for i := 0; i < len(b); i++ {
		var v byte
		for j := 0; j < 2; j++ {
			c := s[i*2+j]
			var nibble byte
			switch {
			case c >= '0' && c <= '9':
				nibble = c - '0'
			case c >= 'a' && c <= 'f':
				nibble = c - 'a' + 10
			case c >= 'A' && c <= 'F':
				nibble = c - 'A' + 10
			}
			v = v<<4 | nibble
		}
		b[i] = v
	}
	return b
}
