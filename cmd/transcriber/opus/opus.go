package opus

// #cgo LDFLAGS: -l:libopus.a -lm
// #include <opus.h>
import "C"

import (
	"fmt"
)

type Decoder struct {
	dec      *C.OpusDecoder
	rate     int
	channels int
}

func NewDecoder(rate, channels int) (*Decoder, error) {
	var d Decoder
	var errCode C.int

	d.dec = C.opus_decoder_create(C.int(rate), C.int(channels), &errCode)
	d.rate = rate
	d.channels = channels

	if errCode != 0 {
		return nil, fmt.Errorf("failed to create opus decoder: %d", errCode)
	}

	return &d, nil
}

func (d *Decoder) Decode(data []byte, samples []float32) (int, error) {
	if d.dec == nil {
		return 0, fmt.Errorf("decoder is not initialized")
	}

	if len(data) == 0 {
		return 0, fmt.Errorf("data should not be empty")
	}

	if len(samples) == 0 {
		return 0, fmt.Errorf("samples should not be empty")
	}

	if cap(samples)%d.channels != 0 {
		return 0, fmt.Errorf("invalid samples capacity")
	}

	ret := int(C.opus_decode_float(d.dec, (*C.uchar)(&data[0]), C.int(len(data)),
		(*C.float)(&samples[0]), C.int(cap(samples)/d.channels), 0))
	if ret < 0 {
		return 0, fmt.Errorf("decode failed with code %d", ret)
	}

	return ret, nil
}

func (d *Decoder) Destroy() error {
	if d.dec == nil {
		return fmt.Errorf("decoder is not initialized")
	}
	C.opus_decoder_destroy(d.dec)
	d.dec = nil
	return nil
}
