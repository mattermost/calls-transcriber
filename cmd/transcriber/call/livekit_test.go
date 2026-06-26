package call

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLivekitIdentity(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		userID, sessionID, err := parseLivekitIdentity("8w8jorhr7j83uqr6y1st894hqe___67t5u6cmtfbb7jug739d43xa9e")
		require.NoError(t, err)
		require.Equal(t, "8w8jorhr7j83uqr6y1st894hqe", userID)
		require.Equal(t, "67t5u6cmtfbb7jug739d43xa9e", sessionID)
	})

	t.Run("missing separator", func(t *testing.T) {
		_, _, err := parseLivekitIdentity("8w8jorhr7j83uqr6y1st894hqe")
		require.Error(t, err)
	})

	t.Run("empty userID", func(t *testing.T) {
		_, _, err := parseLivekitIdentity("___67t5u6cmtfbb7jug739d43xa9e")
		require.Error(t, err)
	})

	t.Run("empty sessionID", func(t *testing.T) {
		_, _, err := parseLivekitIdentity("8w8jorhr7j83uqr6y1st894hqe___")
		require.Error(t, err)
	})

	t.Run("empty", func(t *testing.T) {
		_, _, err := parseLivekitIdentity("")
		require.Error(t, err)
	})
}
