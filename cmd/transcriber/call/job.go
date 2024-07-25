package call

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost-plugin-calls/server/public"
)

func (t *Transcriber) postJobStatus(status public.JobStatus) error {
	apiURL := fmt.Sprintf("%s/plugins/%s/bot/calls/%s/jobs/%s/status",
		t.apiURL, pluginID, t.cfg.CallID, t.cfg.TranscriptionID)

	payload, err := json.Marshal(&status)
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}

	ctx, cancelCtx := context.WithTimeout(context.Background(), httpRequestTimeout)
	defer cancelCtx()
	resp, err := t.apiClient.DoAPIRequestBytes(ctx, http.MethodPost, apiURL, payload, "")
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	cancelCtx()

	return nil
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
