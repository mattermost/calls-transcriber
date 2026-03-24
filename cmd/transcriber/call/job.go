package call

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/mattermost/mattermost-plugin-calls/server/public"
)

const (
	postJobStatusMaxRetries = 2
	postJobStatusRetryDelay = 2 * time.Second
)

func (t *Transcriber) postJobStatus(status public.JobStatus) error {
	apiURL := fmt.Sprintf("%s/plugins/%s/bot/calls/%s/jobs/%s/status",
		t.apiURL, pluginID, t.cfg.CallID, t.cfg.TranscriptionID)

	payload, err := json.Marshal(&status)
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= postJobStatusMaxRetries; attempt++ {
		if attempt > 0 {
			slog.Warn("postJobStatus failed, retrying", slog.Int("attempt", attempt), slog.String("err", lastErr.Error()))
			time.Sleep(postJobStatusRetryDelay)
		}
		ctx, cancelCtx := context.WithTimeout(context.Background(), httpRequestTimeout)
		resp, err := t.apiClient.DoAPIRequestBytes(ctx, http.MethodPost, apiURL, payload, "")
		cancelCtx()
		if err == nil {
			resp.Body.Close()
			return nil
		}
		lastErr = err
	}

	return fmt.Errorf("request failed: %w", lastErr)
}

func (t *Transcriber) ReportJobFailure(errMsg string) error {
	return t.postJobStatus(public.JobStatus{
		JobType: public.JobTypeTranscribing,
		Status:  public.JobStatusTypeFailed,
		Error:   errMsg,
	})
}

func (t *Transcriber) ReportJobStarted() error {
	return t.postJobStatus(public.JobStatus{
		JobType: public.JobTypeTranscribing,
		Status:  public.JobStatusTypeStarted,
	})
}
