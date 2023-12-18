package call

import (
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
)

type trackRemote interface {
	ID() string
	ReadRTP() (*rtp.Packet, interceptor.Attributes, error)
}
