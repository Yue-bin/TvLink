package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultResearchPollInterval = 5 * time.Second

type researchStatus struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

// RunResearch creates a Tavily Research task and polls it with the creating Key.
func (h *Handler) RunResearch(ctx context.Context, body []byte, progress func(string)) ([]byte, error) {
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(body, &arguments); err != nil {
		return nil, fmt.Errorf("decode research arguments: %w", err)
	}
	arguments["stream"] = json.RawMessage("false")
	payload, err := json.Marshal(arguments)
	if err != nil {
		return nil, fmt.Errorf("encode research arguments: %w", err)
	}

	lease, err := h.selector.Select(ctx, time.Now(), estimate("/research", payload))
	if err != nil {
		return nil, err
	}
	response, err := h.researchRequest(ctx, http.MethodPost, "/research", lease.Key.APIKey, payload)
	if err != nil {
		h.pool.Resolve(lease, http.StatusInternalServerError, 0, time.Now())
		return nil, err
	}
	h.pool.Resolve(lease, response.StatusCode, retryAfter(response.Header.Get("Retry-After")), time.Now())
	created, err := readResearchResponse(response)
	if err != nil {
		return nil, err
	}
	status, err := decodeResearchStatus(created)
	if err != nil {
		return nil, err
	}
	if status.RequestID == "" {
		return nil, fmt.Errorf("research response missing request_id")
	}
	h.storeResearchMapping(status.RequestID, lease.Key.Name, time.Now())
	if done, err := researchTerminal(status.Status); done || err != nil {
		return created, err
	}
	if progress != nil {
		progress(status.Status)
	}

	interval := h.researchPollInterval
	if interval <= 0 {
		interval = defaultResearchPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			polled, pollErr := h.pollResearch(ctx, status.RequestID, lease.Key.APIKey)
			if pollErr != nil {
				return nil, pollErr
			}
			pollStatus, decodeErr := decodeResearchStatus(polled)
			if decodeErr != nil {
				return nil, decodeErr
			}
			if pollStatus.RequestID != status.RequestID {
				return nil, fmt.Errorf("research response request_id %q does not match %q", pollStatus.RequestID, status.RequestID)
			}
			if done, terminalErr := researchTerminal(pollStatus.Status); done || terminalErr != nil {
				return polled, terminalErr
			}
			if progress != nil {
				progress(pollStatus.Status)
			}
		}
	}
}

func (h *Handler) pollResearch(ctx context.Context, requestID, apiKey string) ([]byte, error) {
	response, err := h.researchRequest(ctx, http.MethodGet, "/research/"+requestID, apiKey, nil)
	if err != nil {
		return nil, err
	}
	return readResearchResponse(response)
}

func (h *Handler) researchRequest(ctx context.Context, method, path, apiKey string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build research request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send research request: %w", err)
	}
	return response, nil
}

func readResearchResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read research response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("research request returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func decodeResearchStatus(body []byte) (researchStatus, error) {
	var status researchStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return researchStatus{}, fmt.Errorf("decode research response: %w", err)
	}
	return status, nil
}

func researchTerminal(status string) (bool, error) {
	switch status {
	case "completed":
		return true, nil
	case "pending", "in_progress":
		return false, nil
	case "failed":
		return true, fmt.Errorf("research task failed")
	default:
		return true, fmt.Errorf("research task returned unknown status %q", status)
	}
}
