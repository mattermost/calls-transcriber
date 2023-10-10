package call

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	httpRequestTimeout         = 5 * time.Second
	httpUploadTimeout          = 10 * time.Second
	maxUploadRetryAttempts     = 5
	uploadRetryAttemptWaitTime = 5 * time.Second
)

func (t *Transcriber) getUserForSession(sessionID string) (*model.User, error) {
	ctx, cancelFn := context.WithTimeout(context.Background(), httpRequestTimeout)
	defer cancelFn()

	url := fmt.Sprintf("%s/plugins/%s/bot/calls/%s/sessions/%s/profile", t.cfg.SiteURL, pluginID, t.cfg.CallID, sessionID)
	resp, err := t.apiClient.DoAPIRequest(ctx, http.MethodGet, url, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user profile: %w", err)
	}
	defer resp.Body.Close()

	var user *model.User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user profile: %w", err)
	}

	return user, nil
}

func getDataDir() string {
	if dir := os.Getenv("DATA_DIR"); dir != "" {
		return dir
	}
	return dataDir
}

func getModelsDir() string {
	if dir := os.Getenv("MODELS_DIR"); dir != "" {
		return dir
	}
	return modelsDir
}

func (t *Transcriber) publishTranscription(f *os.File) (err error) {
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	apiURL := fmt.Sprintf("%s/plugins/%s/bot", t.apiClient.URL, pluginID)

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	us := &model.UploadSession{
		ChannelId: t.cfg.CallID,
		Filename:  filepath.Base(f.Name()),
		FileSize:  info.Size(),
	}

	payload, err := json.Marshal(us)
	if err != nil {
		return fmt.Errorf("failed to encode payload: %w", err)
	}

	for i := 0; i < maxUploadRetryAttempts; i++ {
		if i > 0 {
			log.Printf("publishTranscription failed, reattempting in %v", uploadRetryAttemptWaitTime)
			time.Sleep(uploadRetryAttemptWaitTime)
		}

		ctx, cancelCtx := context.WithTimeout(context.Background(), httpRequestTimeout)
		defer cancelCtx()
		resp, err := t.apiClient.DoAPIRequestBytes(ctx, http.MethodPost, apiURL+"/uploads", payload, "")
		if err != nil {
			log.Printf("failed to create upload (%d): %s", resp.StatusCode, err)
			continue
		}
		defer resp.Body.Close()
		cancelCtx()

		if err := json.NewDecoder(resp.Body).Decode(&us); err != nil {
			log.Printf("failed to decode response body: %s", err)
			continue
		}

		ctx, cancelCtx = context.WithTimeout(context.Background(), httpUploadTimeout)
		defer cancelCtx()
		resp, err = t.apiClient.DoAPIRequestReader(ctx, http.MethodPost, apiURL+"/uploads/"+us.Id, f, nil)
		if err != nil {
			log.Printf("failed to upload data (%d): %s", resp.StatusCode, err)
			continue
		}
		defer resp.Body.Close()
		cancelCtx()

		var fi model.FileInfo
		if err := json.NewDecoder(resp.Body).Decode(&fi); err != nil {
			log.Printf("failed to decode response body: %s", err)
			continue
		}

		payload, err = json.Marshal(map[string]string{
			"transcription_id": t.cfg.TranscriptionID,
			"file_id":          fi.Id,
			"thread_id":        t.cfg.ThreadID,
		})
		if err != nil {
			log.Printf("failed to encode payload: %s", err)
			continue
		}

		url := fmt.Sprintf("%s/calls/%s/transcriptions", apiURL, t.cfg.CallID)
		ctx, cancelCtx = context.WithTimeout(context.Background(), httpRequestTimeout)
		defer cancelCtx()
		resp, err = t.apiClient.DoAPIRequestBytes(ctx, http.MethodPost, url, payload, "")
		if err != nil {
			log.Printf("failed to post transcription (%d): %s", resp.StatusCode, err)
			continue
		}
		defer resp.Body.Close()

		return nil
	}

	return fmt.Errorf("maximum attempts reached")
}

func newTimeP(t time.Time) *time.Time {
	return &t
}
