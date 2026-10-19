package call

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"

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

	t.Run("metadata never stops the transcriber", func(t *testing.T) {
		tr := setupTranscriberForTest(t)

		// The transcription job showing up and then missing again is not a stop
		// signal: metadata only carries active jobs and can be stale or arrive out
		// of order, so absence is ambiguous.
		tr.handleRoomMetadata(`{"transcription":{"start_at":1}}`)
		tr.handleRoomMetadata(`{"host_id":"h"}`)
		select {
		case <-tr.doneCh:
			t.Fatal("transcriber should not be done")
		case <-time.After(100 * time.Millisecond):
		}
	})
}

func TestSyncStart(t *testing.T) {
	t.Run("recording already started in the join response call state", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		tr.syncStart(liveKitJobs{Recording: &liveKitJobState{StartAt: 1700000000000}})
		select {
		case <-tr.startedCh:
		default:
			t.Fatal("expected transcriber to be started")
		}
		require.Equal(t, time.UnixMilli(1700000000000), *tr.startTime.Load())
	})

	t.Run("recording not started in the join response call state", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		tr.syncStart(liveKitJobs{Recording: &liveKitJobState{}})
		select {
		case <-tr.startedCh:
			t.Fatal("transcriber should not be started")
		default:
		}
		require.Nil(t, tr.startTime.Load())
	})

	t.Run("no recording", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		tr.syncStart(liveKitJobs{})
		select {
		case <-tr.startedCh:
			t.Fatal("transcriber should not be started")
		default:
		}
	})
}

func TestHandleDisconnect(t *testing.T) {
	tcs := []struct {
		reason     lksdk.DisconnectionReason
		stopReason string
	}{
		{lksdk.ParticipantRemoved, ""},
		{lksdk.RoomClosed, ""},
		{lksdk.LeaveRequested, ""},
		{lksdk.Failed, "livekit room disconnected unexpectedly: connection to room failed"},
		{lksdk.DuplicateIdentity, "livekit room disconnected unexpectedly: duplicate identity"},
		{lksdk.OtherReason, "livekit room disconnected unexpectedly: other reasons"},
	}

	for _, tc := range tcs {
		t.Run(string(tc.reason), func(t *testing.T) {
			tr := setupTranscriberForTest(t)
			tr.handleDisconnect(tc.reason)
			select {
			case <-tr.doneCh:
			case <-time.After(2 * time.Second):
				t.Fatal("expected transcriber to be done")
			}
			require.Equal(t, tc.stopReason, tr.StopReason())
		})
	}
}

type fakeRoom struct {
	metadata    string
	disconnects atomic.Int32
}

func (f *fakeRoom) Disconnect() {
	f.disconnects.Add(1)
}

func (f *fakeRoom) Metadata() string {
	return f.metadata
}

func TestSetRoom(t *testing.T) {
	t.Run("done after the room is set disconnects it", func(t *testing.T) {
		tr := setupTranscriberForTest(t)
		room := &fakeRoom{}
		tr.setRoom(room)
		require.Zero(t, room.disconnects.Load())

		tr.done("")
		require.Equal(t, int32(1), room.disconnects.Load())
	})

	t.Run("done before the room is set disconnects it when it is set", func(t *testing.T) {
		tr := setupTranscriberForTest(t)

		// A LiveKit callback stopping the transcriber before ConnectToRoomWithToken
		// has returned.
		tr.done("")

		room := &fakeRoom{}
		tr.setRoom(room)
		require.Equal(t, int32(1), room.disconnects.Load())
	})
}
