//go:build windows

package audio

import "testing"

// blob builds what a driver hands back through PKEY_AudioEngine_DeviceFormat: a
// packed WAVEFORMATEX of eighteen bytes, cbSize at offset 16, and whatever
// extra bytes are really there — which is the number this guard is about, since
// the structure declares it about itself.
func blob(cbSize, extraPresent int) []byte {
	b := make([]byte, 18+extraPresent)
	b[0], b[1] = 0xFE, 0xFF // wFormatTag: WAVE_FORMAT_EXTENSIBLE
	b[2] = 2                // nChannels
	b[16] = byte(cbSize)
	b[17] = byte(cbSize >> 8)
	return b
}

// **A blob is believed about its length only as far as its length goes.**
//
// The check used to be `size >= 18`, and the read that follows it is the
// SubFormat GUID at offset 24, authorised by cbSize — a number inside the blob.
// Eighteen bytes declaring twenty-two more therefore sent decodeFormat
// twenty-two bytes past the end of a Go slice, and handed the same pointer to
// IsFormatSupported, where WASAPI does the reading.
//
// **The defect was put back and this test fails with it**: with the length check
// reduced to eighteen, the first two cases below are accepted.
func TestAFormatBlobIsMeasuredBeforeItIsBelieved(t *testing.T) {
	cases := []struct {
		name    string
		b       []byte
		wantErr bool
	}{
		{"eighteen bytes claiming twenty-two more", blob(22, 0), true},
		{"the GUID half outside", blob(22, 12), true},
		{"shorter than a WAVEFORMATEX", make([]byte, 17), true},
		{"empty", nil, true},
		{"a plain PCM format, nothing beyond", blob(0, 0), false},
		{"a whole WAVEFORMATEXTENSIBLE", blob(22, 22), false},
		{"more bytes than declared", blob(22, 40), false},
	}
	for _, c := range cases {
		err := validateFormatBlob(c.b)
		if c.wantErr && err == nil {
			t.Errorf("%s: accepted, and the SubFormat GUID is then read off the "+
				"end of the buffer", c.name)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s: refused (%v), and a device that declares its format "+
				"properly must still open", c.name, err)
		}
	}
}

// The offset the check protects is the one decodeFormat really reads from, and
// the two are written in different places: if the GUID ever moves, this says so
// rather than leaving the bound describing a layout nobody uses any more.
func TestTheGuardCoversTheOffsetThatIsRead(t *testing.T) {
	const guidLen = 16
	if need := subFormatOffset + guidLen; need > waveFormatExSizeC+22 {
		t.Errorf("the SubFormat GUID ends at %d, past the %d a cbSize of 22 "+
			"guarantees: the minimum in decodeFormat no longer covers the read",
			need, waveFormatExSizeC+22)
	}
}
