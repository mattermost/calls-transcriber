package call

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/mattermost/mattermost/server/public/model"
)

// mmAPIClient adapts *model.Client4 to APIClient. mattermost/server/public v0.6
// unexported Client4.DoAPIRequest*; these methods keep the previous behavior of
// calling a fully-qualified plugin URL and turning non-2xx responses into an AppError.
type mmAPIClient struct {
	*model.Client4
}

func (c mmAPIClient) DoAPIRequest(ctx context.Context, method, rawURL, data, etag string) (*http.Response, error) {
	return c.DoAPIRequestBytes(ctx, method, rawURL, []byte(data), etag)
}

func (c mmAPIClient) DoAPIRequestBytes(ctx context.Context, method, rawURL string, data []byte, etag string) (*http.Response, error) {
	return c.DoAPIRequestReader(ctx, method, rawURL, bytes.NewReader(data), map[string]string{
		model.HeaderEtagClient: etag,
	})
}

func (c mmAPIClient) DoAPIRequestReader(ctx context.Context, method, rawURL string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.AuthToken != "" {
		req.Header.Set(model.HeaderAuth, c.AuthType+" "+c.AuthToken)
	}
	for k, v := range c.HTTPHeader {
		req.Header.Set(k, v)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode == http.StatusNotModified {
		return resp, nil
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return resp, model.AppErrorFromJSON(resp.Body)
	}

	return resp, nil
}
