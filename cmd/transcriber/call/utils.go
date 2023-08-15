package call

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	httpRequestTimeout = 5 * time.Second
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
