package opus

/*
#cgo linux LDFLAGS: -l:libopus.a -lm
#cgo darwin LDFLAGS: -lopus
#include <opus.h>

int bridge_encoder_set_bitrate(OpusEncoder *st, opus_int32 bitrate) {
	return opus_encoder_ctl(st, OPUS_SET_BITRATE(bitrate));
}

int bridge_encoder_set_fec(OpusEncoder *st, opus_int32 value) {
	return opus_encoder_ctl(st, OPUS_GET_INBAND_FEC(&value));
}
*/
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

type Encoder struct {
	enc      *C.OpusEncoder
	rate     int
	channels int
}

func NewEncoder(rate, channels int) (*Encoder, error) {
	var e Encoder
	var errCode C.int

	e.enc = C.opus_encoder_create(C.int(rate), C.int(channels), C.OPUS_APPLICATION_VOIP, &errCode)
	e.rate = rate
	e.channels = channels

	if errCode != 0 {
		return nil, fmt.Errorf("failed to create opus encoder: %d", errCode)
	}

	errCode = C.bridge_encoder_set_bitrate(e.enc, C.int(40000))
	if errCode != 0 {
		return nil, fmt.Errorf("failed to set encoder bitrate: %d", errCode)
	}

	errCode = C.bridge_encoder_set_fec(e.enc, C.opus_int32(1))
	if errCode != 0 {
		return nil, fmt.Errorf("failed to set in-band fec: %d", errCode)
	}

	return &e, nil
}

func (e *Encoder) Encode(samples []int16, data []byte, frameSize int) (int, error) {
	if e.enc == nil {
		return 0, fmt.Errorf("encoder is not initialized")
	}

	if len(data) == 0 {
		return 0, fmt.Errorf("data should not be empty")
	}

	if len(samples) == 0 {
		return 0, fmt.Errorf("samples should not be empty")
	}

	ret := int(C.opus_encode(e.enc, (*C.opus_int16)(&samples[0]), C.int(frameSize), (*C.uchar)(&data[0]), C.int(len(data))))
	if ret < 0 {
		return 0, fmt.Errorf("encode failed with code %d", ret)
	}

	return ret, nil
}

func (e *Encoder) Destroy() error {
	if e.enc == nil {
		return fmt.Errorf("encoder is not initialized")
	}
	C.opus_encoder_destroy(e.enc)
	e.enc = nil
	return nil
}
