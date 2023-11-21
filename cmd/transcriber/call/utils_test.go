package call

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeFilename(t *testing.T) {
	tcs := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "empty string",
		},
		{
			name:     "spaces",
			input:    "some file name with spaces.mp4",
			expected: "some_file_name_with_spaces.mp4",
		},
		{
			name:     "special chars",
			input:    "somefile*with??special/\\chars.mp4",
			expected: "somefile_with__special__chars.mp4",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, sanitizeFilename(tc.input))
		})
	}
}
