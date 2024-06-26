package azure

import (
	"encoding/binary"
)

// Util to wrap our float32 samples in a WAV (16-bit PCM, mono, 16KHz)
func f32PCMToWAV(samples []float32) []byte {
	wavHeaderLen := 44
	wav := make([]byte, wavHeaderLen+len(samples)*2)
	pcm := wav[wavHeaderLen:]

	// WAV Header
	wav[0] = 'R'
	wav[1] = 'I'
	wav[2] = 'F'
	wav[3] = 'F'
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	wav[8] = 'W'
	wav[9] = 'A'
	wav[10] = 'V'
	wav[11] = 'E'
	wav[12] = 'f'
	wav[13] = 'm'
	wav[14] = 't'
	wav[15] = ' '
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], audioChannels)
	binary.LittleEndian.PutUint32(wav[24:], audioSampleRate)
	binary.LittleEndian.PutUint32(wav[28:], (audioSampleRate*audioBitDepth*audioChannels)/8)
	binary.LittleEndian.PutUint16(wav[32:], (audioBitDepth*audioChannels)/8)
	binary.LittleEndian.PutUint16(wav[34:], audioBitDepth)
	wav[36] = 'd'
	wav[37] = 'a'
	wav[38] = 't'
	wav[39] = 'a'
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(samples)*2))

	// Convert audio samples from float32 samples to uint16 PCM
	for i, s := range samples {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(s*32768.0))
	}

	return wav
}
