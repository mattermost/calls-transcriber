package call

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	mocks "github.com/mattermost/calls-transcriber/cmd/transcriber/mocks/github.com/mattermost/calls-transcriber/cmd/transcriber/call"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCreateLiveKitSession(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		mockClient := &mocks.MockAPIClient{}
		tr.apiClient = mockClient
		defer mockClient.AssertExpectations(t)

		mockClient.On("DoAPIRequestBytes", mock.Anything, http.MethodPost,
			"http://localhost:8065/plugins/com.mattermost.calls/livekit-token",
			[]byte(`{"channel_id":"8w8jorhr7j83uqr6y1st894hqe","job_id":"67t5u6cmtfbb7jug739d43xa9e"}`), "").
			Return(&http.Response{
				Body: io.NopCloser(strings.NewReader(`{"session_id":"sid","token":"tok","url":"ws://lk",
					"call_state":{"recording":{"start_at":1234,"end_at":0}}}`)),
			}, nil).Once()

		session, err := tr.createLiveKitSession(context.Background())
		require.NoError(t, err)
		require.Equal(t, "sid", session.SessionID)
		require.Equal(t, "tok", session.Token)
		require.Equal(t, "ws://lk", session.URL)
		require.NotNil(t, session.CallState.Recording)
		require.Equal(t, int64(1234), session.CallState.Recording.StartAt)
	})

	t.Run("empty token", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		mockClient := &mocks.MockAPIClient{}
		tr.apiClient = mockClient
		defer mockClient.AssertExpectations(t)

		mockClient.On("DoAPIRequestBytes", mock.Anything, http.MethodPost, mock.Anything, mock.Anything, "").
			Return(&http.Response{Body: io.NopCloser(strings.NewReader(`{"url":"ws://lk"}`))}, nil).Once()

		_, err := tr.createLiveKitSession(context.Background())
		require.EqualError(t, err, "invalid livekit session response: empty url or token")
	})
}

func TestHandleRoomMetadata(t *testing.T) {
	isDone := func(tr *Transcriber) bool {
		select {
		case <-tr.doneCh:
			return true
		case <-time.After(500 * time.Millisecond):
			return false
		}
	}
	isStarted := func(tr *Transcriber) bool {
		select {
		case <-tr.startedCh:
			return true
		default:
			return false
		}
	}

	t.Run("empty and invalid metadata are ignored", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		tr.handleRoomMetadata("")
		tr.handleRoomMetadata("{not json")
		require.False(t, isStarted(tr))
	})

	t.Run("recording not started", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		tr.handleRoomMetadata(`{"recording":{"start_at":0,"end_at":0},"transcription":{"start_at":0}}`)
		require.False(t, isStarted(tr))
		require.Nil(t, tr.startTime.Load())
	})

	t.Run("recording started syncs start time once", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		tr.handleRoomMetadata(`{"recording":{"start_at":1700000000000},"transcription":{"start_at":1}}`)
		require.True(t, isStarted(tr))
		require.Equal(t, time.UnixMilli(1700000000000), *tr.startTime.Load())

		// A later change must not move the start time or panic on a double close.
		tr.handleRoomMetadata(`{"recording":{"start_at":1700000009999},"transcription":{"start_at":1}}`)
		require.Equal(t, time.UnixMilli(1700000000000), *tr.startTime.Load())
	})

	t.Run("transcription absent before being seen is not a stop", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		tr.handleRoomMetadata(`{"host_id":"h"}`)
		require.False(t, isStarted(tr))
		select {
		case <-tr.doneCh:
			t.Fatal("transcriber should not be done")
		case <-time.After(100 * time.Millisecond):
		}
	})

	t.Run("transcription disappearing after being seen stops", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		tr.handleRoomMetadata(`{"transcription":{"start_at":1}}`)
		tr.handleRoomMetadata(`{"host_id":"h"}`)
		require.True(t, isDone(tr))
		require.Equal(t, "", tr.StopReason())
	})
}
