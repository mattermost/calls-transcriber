package call

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/transcribe"

	"github.com/mattermost/mattermost-plugin-calls/server/public"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	httpRequestTimeout         = 5 * time.Second
	httpUploadTimeout          = 10 * time.Second
	maxUploadRetryAttempts     = 5
	uploadRetryAttemptWaitTime = 5 * time.Second
)

var (
	filenameSanitizationRE = regexp.MustCompile(`[\\:*?\"<>|\n\s/]`)
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

func (t *Transcriber) publishTranscription(tr transcribe.Transcription) (err error) {
	fname, err := t.getFilenameForCall()
	if err != nil {
		return fmt.Errorf("failed to get filename for call: %w", err)
	}

	vttFile, err := os.OpenFile(filepath.Join(getDataDir(), fname+".vtt"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	defer vttFile.Close()

	textFile, err := os.OpenFile(filepath.Join(getDataDir(), fname+".txt"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	defer textFile.Close()

	if err := tr.WebVTT(vttFile, t.cfg.OutputOptions.WebVTT); err != nil {
		return fmt.Errorf("failed to write WebVTT file: %w", err)
	}

	if err := tr.Text(textFile, t.cfg.OutputOptions.Text); err != nil {
		return fmt.Errorf("failed to write text file: %w", err)
	}

	if _, err := vttFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	if _, err := textFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	vttInfo, err := vttFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	textInfo, err := textFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	apiURL := fmt.Sprintf("%s/plugins/%s/bot", t.apiClient.URL, pluginID)

	for i := 0; i < maxUploadRetryAttempts; i++ {
		if i > 0 {
			slog.Error("publishTranscription failed", slog.Duration("reattempt_time", uploadRetryAttemptWaitTime))
			time.Sleep(uploadRetryAttemptWaitTime)
		}

		// VTT format upload
		us := &model.UploadSession{
			ChannelId: t.cfg.CallID,
			Filename:  filepath.Base(vttFile.Name()),
			FileSize:  vttInfo.Size(),
		}

		payload, err := json.Marshal(us)
		if err != nil {
			return fmt.Errorf("failed to encode payload: %w", err)
		}

		ctx, cancelCtx := context.WithTimeout(context.Background(), httpRequestTimeout)
		defer cancelCtx()
		resp, err := t.apiClient.DoAPIRequestBytes(ctx, http.MethodPost, apiURL+"/uploads", payload, "")
		if err != nil {
			slog.Error("failed to create upload", slog.String("err", err.Error()))
			continue
		}
		defer resp.Body.Close()
		cancelCtx()

		if err := json.NewDecoder(resp.Body).Decode(&us); err != nil {
			slog.Error("failed to decode response body", slog.String("err", err.Error()))
			continue
		}

		ctx, cancelCtx = context.WithTimeout(context.Background(), httpUploadTimeout)
		defer cancelCtx()
		resp, err = t.apiClient.DoAPIRequestReader(ctx, http.MethodPost, apiURL+"/uploads/"+us.Id, vttFile, nil)
		if err != nil {
			slog.Error("failed to upload data", slog.String("err", err.Error()))
			continue
		}
		defer resp.Body.Close()
		cancelCtx()

		var vttFi model.FileInfo
		if err := json.NewDecoder(resp.Body).Decode(&vttFi); err != nil {
			slog.Error("failed to decode response body", slog.String("err", err.Error()))
			continue
		}

		// text format upload
		us = &model.UploadSession{
			ChannelId: t.cfg.CallID,
			Filename:  filepath.Base(textFile.Name()),
			FileSize:  textInfo.Size(),
		}

		payload, err = json.Marshal(us)
		if err != nil {
			return fmt.Errorf("failed to encode payload: %w", err)
		}

		ctx, cancelCtx = context.WithTimeout(context.Background(), httpRequestTimeout)
		defer cancelCtx()
		resp, err = t.apiClient.DoAPIRequestBytes(ctx, http.MethodPost, apiURL+"/uploads", payload, "")
		if err != nil {
			slog.Error("failed to create upload", slog.String("err", err.Error()))
			continue
		}
		defer resp.Body.Close()
		cancelCtx()

		if err := json.NewDecoder(resp.Body).Decode(&us); err != nil {
			slog.Error("failed to decode response body", slog.String("err", err.Error()))
			continue
		}

		ctx, cancelCtx = context.WithTimeout(context.Background(), httpUploadTimeout)
		defer cancelCtx()
		resp, err = t.apiClient.DoAPIRequestReader(ctx, http.MethodPost, apiURL+"/uploads/"+us.Id, textFile, nil)
		if err != nil {
			slog.Error("failed to upload data", slog.String("err", err.Error()))
			continue
		}
		defer resp.Body.Close()
		cancelCtx()

		var textFi model.FileInfo
		if err := json.NewDecoder(resp.Body).Decode(&textFi); err != nil {
			slog.Error("failed to decode response body", slog.String("err", err.Error()))
			continue
		}

		// attaching post VTT and text formatted files.
		payload, err = json.Marshal(public.JobInfo{
			JobID:   t.cfg.TranscriptionID,
			FileIDs: []string{vttFi.Id, textFi.Id},
			PostID:  t.cfg.PostID,
		})
		if err != nil {
			slog.Error("failed to encode payload", slog.String("err", err.Error()))
			continue
		}

		url := fmt.Sprintf("%s/calls/%s/transcriptions", apiURL, t.cfg.CallID)
		ctx, cancelCtx = context.WithTimeout(context.Background(), httpRequestTimeout)
		defer cancelCtx()
		resp, err = t.apiClient.DoAPIRequestBytes(ctx, http.MethodPost, url, payload, "")
		if err != nil {
			slog.Error("failed to post transcription", slog.String("err", err.Error()))
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

func sanitizeFilename(name string) string {
	return filenameSanitizationRE.ReplaceAllString(name, "_")
}

func (t *Transcriber) getFilenameForCall() (string, error) {
	ctx, cancelFn := context.WithTimeout(context.Background(), httpRequestTimeout)
	defer cancelFn()

	url := fmt.Sprintf("%s/plugins/%s/bot/calls/%s/filename", t.cfg.SiteURL, pluginID, t.cfg.CallID)
	resp, err := t.apiClient.DoAPIRequest(ctx, http.MethodGet, url, "", "")
	if err != nil {
		return "", fmt.Errorf("failed to get filename: %w", err)
	}
	defer resp.Body.Close()

	var m map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return "", fmt.Errorf("failed to unmarshal filename: %w", err)
	}

	filename := sanitizeFilename(m["filename"])

	if filename == "" {
		return "", fmt.Errorf("invalid empty filename")
	}

	return filename, nil
}
