package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var InternalHTTPClient = &http.Client{Timeout: 5 * time.Second}

func InternalJSON(ctx context.Context, method, url, serviceKey string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Service-Key", serviceKey)
	response, err := InternalHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("internal request %s returned %d: %s", url, response.StatusCode, string(payload))
	}
	if out == nil {
		return nil
	}
	var envelope APIResponse
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return err
	}
	payload, err := json.Marshal(envelope.Data)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, out)
}
