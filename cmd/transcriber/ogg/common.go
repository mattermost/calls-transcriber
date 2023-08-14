package ogg

const (
	pageHeaderTypeContinuationOfStream = 0x00
	pageHeaderTypeBeginningOfStream    = 0x02
	pageHeaderTypeEndOfStream          = 0x04
	defaultPreSkip                     = 3840 // 3840 recommended in the RFC
	idPageSignature                    = "OpusHead"
	commentPageSignature               = "OpusTags"
	pageHeaderSignature                = "OggS"
)

func generateChecksumTable() *[256]uint32 {
	var table [256]uint32
	const poly = 0x04c11db7

	for i := range table {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if (r & 0x80000000) != 0 {
				r = (r << 1) ^ poly
			} else {
				r <<= 1
			}
			table[i] = (r & 0xffffffff)
		}
	}
	return &table
}
