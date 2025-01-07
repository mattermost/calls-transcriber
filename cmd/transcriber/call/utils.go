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
	httpRequestTimeout          = 5 * time.Second
	httpUploadTimeout           = 10 * time.Second
	uploadRetryAttemptWaitTime  = 5 * time.Second
	getUserRetryAttemptWaitTime = time.Second
)

var (
	filenameSanitizationRE = regexp.MustCompile(`[\\:*?\"<>|\n\s/]`)
	maxAPIRetryAttempts    = 5
)

func (t *Transcriber) getUserForSession(sessionID string) (*model.User, error) {
	getUser := func() (*model.User, error) {
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

	for i := 0; i < maxAPIRetryAttempts; i++ {
		user, err := getUser()
		if err == nil {
			return user, nil
		}

		slog.Error("getUserForSession failed",
			slog.String("err", err.Error()),
			slog.Duration("reattempt_time", getUserRetryAttemptWaitTime))

		time.Sleep(getUserRetryAttemptWaitTime)
	}

	return nil, fmt.Errorf("failed to get user for call: max attempts reached")
}

func GetDataDir(jobID string) string {
	if dir := os.Getenv("DATA_DIR"); dir != "" {
		return filepath.Join(dir, jobID)
	}
	return filepath.Join(dataDir, jobID)
}

func getModelsDir() string {
	if dir := os.Getenv("MODELS_DIR"); dir != "" {
		return dir
	}
	return modelsDir
}

func (t *Transcriber) publishTranscription(tr transcribe.Transcription) (err error) {
	var fname string
	for i := 0; i < maxAPIRetryAttempts; i++ {
		if i > 0 {
			slog.Error("getFilenameForCall failed",
				slog.String("err", err.Error()),
				slog.Duration("reattempt_time", uploadRetryAttemptWaitTime))
			time.Sleep(uploadRetryAttemptWaitTime)
		}

		fname, err = t.getFilenameForCall()
		if err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("failed to get filename for call: %w", err)
	}

	var vttFile *os.File
	var textFile *os.File
	openFiles := func() error {
		vttFile, err = os.OpenFile(filepath.Join(t.dataPath, fname+".vtt"), os.O_RDWR|os.O_CREATE, 0600)
		if err != nil {
			return fmt.Errorf("failed to open output file: %w", err)
		}

		textFile, err = os.OpenFile(filepath.Join(t.dataPath, fname+".txt"), os.O_RDWR|os.O_CREATE, 0600)
		if err != nil {
			return fmt.Errorf("failed to open output file: %w", err)
		}

		return nil
	}

	if err := openFiles(); err != nil {
		return err
	}
	defer vttFile.Close()
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

	apiURL := fmt.Sprintf("%s/plugins/%s/bot", t.apiURL, pluginID)

	var lastErr error
	for i := 0; i < maxAPIRetryAttempts; i++ {
		if i > 0 {
			slog.Error("publishTranscription failed", slog.Duration("reattempt_time", uploadRetryAttemptWaitTime))
			time.Sleep(uploadRetryAttemptWaitTime)
			if err := openFiles(); err != nil {
				return fmt.Errorf("failed to open files: %w", err)
			}
			defer vttFile.Close()
			defer textFile.Close()
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
			lastErr = err
			continue
		}
		defer resp.Body.Close()
		cancelCtx()

		if err := json.NewDecoder(resp.Body).Decode(&us); err != nil {
			slog.Error("failed to decode response body", slog.String("err", err.Error()))
			lastErr = err
			continue
		}

		ctx, cancelCtx = context.WithTimeout(context.Background(), httpUploadTimeout)
		defer cancelCtx()
		resp, err = t.apiClient.DoAPIRequestReader(ctx, http.MethodPost, apiURL+"/uploads/"+us.Id, vttFile, nil)
		if err != nil {
			slog.Error("failed to upload data", slog.String("err", err.Error()))
			lastErr = err
			continue
		}
		defer resp.Body.Close()
		cancelCtx()

		var vttFi model.FileInfo
		if err := json.NewDecoder(resp.Body).Decode(&vttFi); err != nil {
			slog.Error("failed to decode response body", slog.String("err", err.Error()))
			lastErr = err
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
			lastErr = err
			continue
		}
		defer resp.Body.Close()
		cancelCtx()

		if err := json.NewDecoder(resp.Body).Decode(&us); err != nil {
			slog.Error("failed to decode response body", slog.String("err", err.Error()))
			lastErr = err
			continue
		}

		ctx, cancelCtx = context.WithTimeout(context.Background(), httpUploadTimeout)
		defer cancelCtx()
		resp, err = t.apiClient.DoAPIRequestReader(ctx, http.MethodPost, apiURL+"/uploads/"+us.Id, textFile, nil)
		if err != nil {
			slog.Error("failed to upload data", slog.String("err", err.Error()))
			lastErr = err
			continue
		}
		defer resp.Body.Close()
		cancelCtx()

		var textFi model.FileInfo
		if err := json.NewDecoder(resp.Body).Decode(&textFi); err != nil {
			slog.Error("failed to decode response body", slog.String("err", err.Error()))
			lastErr = err
			continue
		}

		// attaching post VTT and text formatted files.
		payload, err = json.Marshal(public.TranscribingJobInfo{
			JobID:  t.cfg.TranscriptionID,
			PostID: t.cfg.PostID,
			Transcriptions: []public.Transcription{
				{
					Language: tr.Language(),
					FileIDs:  []string{vttFi.Id, textFi.Id},
				},
			},
		})
		if err != nil {
			slog.Error("failed to encode payload", slog.String("err", err.Error()))
			lastErr = err
			continue
		}

		url := fmt.Sprintf("%s/calls/%s/transcriptions", apiURL, t.cfg.CallID)
		ctx, cancelCtx = context.WithTimeout(context.Background(), httpRequestTimeout)
		defer cancelCtx()
		resp, err = t.apiClient.DoAPIRequestBytes(ctx, http.MethodPost, url, payload, "")
		if err != nil {
			slog.Error("failed to post transcription", slog.String("err", err.Error()))
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		return nil
	}

	return fmt.Errorf("maximum attempts reached : %w", lastErr)
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
