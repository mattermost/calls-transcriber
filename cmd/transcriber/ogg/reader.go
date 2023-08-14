// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

// Package oggreader implements the Ogg media container reader
package ogg

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	pageHeaderLen       = 27
	idPagePayloadLength = 19
)

var (
	errNilStream                 = errors.New("stream is nil")
	errBadIDPageSignature        = errors.New("bad header signature")
	errBadIDPageType             = errors.New("wrong header, expected beginning of stream")
	errBadIDPageLength           = errors.New("payload for id page must be 19 bytes")
	errBadIDPagePayloadSignature = errors.New("bad payload signature")
	errShortPageHeader           = errors.New("not enough data for payload header")
	errChecksumMismatch          = errors.New("expected and actual checksum do not match")
)

// Reader is used to read Ogg files and return page payloads
type Reader struct {
	stream               io.Reader
	bytesReadSuccesfully int64
	checksumTable        *[256]uint32
	doChecksum           bool
}

// Header is the metadata from the first two pages
// in the file (ID and Comment)
//
// https://tools.ietf.org/html/rfc7845.html#section-3
type Header struct {
	ChannelMap uint8
	Channels   uint8
	OutputGain uint16
	PreSkip    uint16
	SampleRate uint32
	Version    uint8
}

// PageHeader is the metadata for a Page
// Pages are the fundamental unit of multiplexing in an Ogg stream
//
// https://tools.ietf.org/html/rfc7845.html#section-1
type PageHeader struct {
	GranulePosition uint64

	sig           [4]byte
	version       uint8
	headerType    uint8
	serial        uint32
	index         uint32
	segmentsCount uint8
}

// NewReaderWith returns a new Ogg reader and Ogg header
// with an io.Reader input
func NewReaderWith(in io.Reader) (*Reader, *Header, error) {
	return newWith(in /* doChecksum */, true)
}

func newWith(in io.Reader, doChecksum bool) (*Reader, *Header, error) {
	if in == nil {
		return nil, nil, errNilStream
	}

	reader := &Reader{
		stream:        in,
		checksumTable: generateChecksumTable(),
		doChecksum:    doChecksum,
	}

	header, err := reader.readHeaders()
	if err != nil {
		return nil, nil, err
	}

	return reader, header, nil
}

func (o *Reader) readHeaders() (*Header, error) {
	payload, pageHeader, err := o.ParseNextPage()
	if err != nil {
		return nil, err
	}

	header := &Header{}
	if string(pageHeader.sig[:]) != pageHeaderSignature {
		return nil, errBadIDPageSignature
	}

	if pageHeader.headerType != pageHeaderTypeBeginningOfStream {
		return nil, errBadIDPageType
	}

	if len(payload) != idPagePayloadLength {
		return nil, errBadIDPageLength
	}

	if s := string(payload[:8]); s != idPageSignature {
		return nil, errBadIDPagePayloadSignature
	}

	header.Version = payload[8]
	header.Channels = payload[9]
	header.PreSkip = binary.LittleEndian.Uint16(payload[10:12])
	header.SampleRate = binary.LittleEndian.Uint32(payload[12:16])
	header.OutputGain = binary.LittleEndian.Uint16(payload[16:18])
	header.ChannelMap = payload[18]

	return header, nil
}

// ParseNextPage reads from stream and returns Ogg page payload, header,
// and an error if there is incomplete page data.
func (o *Reader) ParseNextPage() ([]byte, *PageHeader, error) {
	h := make([]byte, pageHeaderLen)

	n, err := io.ReadFull(o.stream, h)
	if err != nil {
		return nil, nil, err
	} else if n < len(h) {
		return nil, nil, errShortPageHeader
	}

	pageHeader := &PageHeader{
		sig: [4]byte{h[0], h[1], h[2], h[3]},
	}

	pageHeader.version = h[4]
	pageHeader.headerType = h[5]
	pageHeader.GranulePosition = binary.LittleEndian.Uint64(h[6 : 6+8])
	pageHeader.serial = binary.LittleEndian.Uint32(h[14 : 14+4])
	pageHeader.index = binary.LittleEndian.Uint32(h[18 : 18+4])
	pageHeader.segmentsCount = h[26]

	sizeBuffer := make([]byte, pageHeader.segmentsCount)
	if _, err = io.ReadFull(o.stream, sizeBuffer); err != nil {
		return nil, nil, err
	}

	payloadSize := 0
	for _, s := range sizeBuffer {
		payloadSize += int(s)
	}

	payload := make([]byte, payloadSize)
	if _, err = io.ReadFull(o.stream, payload); err != nil {
		return nil, nil, err
	}

	if o.doChecksum {
		var checksum uint32
		updateChecksum := func(v byte) {
			checksum = (checksum << 8) ^ o.checksumTable[byte(checksum>>24)^v]
		}

		for index := range h {
			// Don't include expected checksum in our generation
			if index > 21 && index < 26 {
				updateChecksum(0)
				continue
			}

			updateChecksum(h[index])
		}
		for _, s := range sizeBuffer {
			updateChecksum(s)
		}
		for index := range payload {
			updateChecksum(payload[index])
		}

		if binary.LittleEndian.Uint32(h[22:22+4]) != checksum {
			return nil, nil, errChecksumMismatch
		}
	}

	return payload, pageHeader, nil
}

// ResetReader resets the internal stream of Reader. This is useful
// for live streams, where the end of the file might be read without the
// data being finished.
func (o *Reader) ResetReader(reset func(bytesRead int64) io.Reader) {
	o.stream = reset(o.bytesReadSuccesfully)
}
