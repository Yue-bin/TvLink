package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tvlink/internal/pool"
)

const defaultResearchPollInterval = 5 * time.Second

type researchStatus struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

type researchAdmission struct {
	response *http.Response
	lease    pool.Lease
}

type upstreamResponseError struct {
	status int
	header http.Header
	body   []byte
}

func (e *upstreamResponseError) Error() string {
	return fmt.Sprintf("research request returned %d: %s", e.status, strings.TrimSpace(string(e.body)))
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

	admission, err := h.admitResearch(ctx, payload, nil)
	if err != nil {
		return nil, err
	}
	created, err := readResearchResponse(admission.response)
	if err != nil {
		h.deferResearchSettlement(admission.lease)
		return nil, err
	}
	status, err := decodeResearchStatus(created)
	if err != nil {
		h.deferResearchSettlement(admission.lease)
		return nil, err
	}
	if status.RequestID == "" {
		h.deferResearchSettlement(admission.lease)
		return nil, fmt.Errorf("research response missing request_id")
	}
	h.storeResearchMapping(status.RequestID, admission.lease, time.Now())
	if done, err := researchTerminal(status.Status); done || err != nil {
		h.settleResearch(ctx, admission.lease)
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
			polled, pollErr := h.pollResearch(ctx, status.RequestID, admission.lease.Key.APIKey)
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
				h.settleResearch(ctx, admission.lease)
				return polled, terminalErr
			}
			if progress != nil {
				progress(pollStatus.Status)
			}
		}
	}
}

func (h *Handler) admitResearch(ctx context.Context, payload []byte, headers http.Header) (researchAdmission, error) {
	selection := pool.Selection{
		Estimate: estimate("/research", payload),
		Workload: pool.WorkloadResearch,
		Excluded: make(map[string]struct{}),
	}
	var lastQuota *upstreamResponseError
	attempt := 0
	for {
		lease, err := h.selector.SelectFor(ctx, time.Now(), selection)
		if err != nil {
			if lastQuota != nil {
				slog.Warn("all research candidates rejected quota", "attempts", attempt, "status", lastQuota.status)
				return researchAdmission{}, lastQuota
			}
			return researchAdmission{}, err
		}
		attempt++
		selection.Excluded[lease.Key.Name] = struct{}{}
		response, err := h.researchRequest(ctx, http.MethodPost, "/research", lease.Key.APIKey, payload, headers)
		if err != nil {
			h.deferResearchSettlement(lease)
			return researchAdmission{}, err
		}
		if response.StatusCode != 432 && response.StatusCode != 433 {
			h.pool.Resolve(lease, response.StatusCode, retryAfter(response.Header.Get("Retry-After")), time.Now())
			return researchAdmission{response: response, lease: lease}, nil
		}
		lastQuota = readUpstreamResponseError(response)
		h.pool.Resolve(lease, response.StatusCode, 0, time.Now())
		slog.Warn("research quota rejected", "key", lease.Key.Name, "status", response.StatusCode, "reservation", lease.Estimate, "attempt", attempt)
	}
}

func (h *Handler) serveResearchCreate(w http.ResponseWriter, r *http.Request, payload []byte) {
	admission, err := h.admitResearch(r.Context(), payload, r.Header)
	if err != nil {
		var quota *upstreamResponseError
		if errors.As(err, &quota) {
			copyHeader(w.Header(), quota.header)
			w.WriteHeader(quota.status)
			_, _ = w.Write(quota.body)
			return
		}
		http.Error(w, "no Tavily key is currently available", http.StatusServiceUnavailable)
		return
	}
	defer admission.response.Body.Close()
	if strings.Contains(admission.response.Header.Get("Content-Type"), "text/event-stream") {
		copyHeader(w.Header(), admission.response.Header)
		w.WriteHeader(admission.response.StatusCode)
		if _, copyErr := io.Copy(w, admission.response.Body); copyErr != nil {
			h.deferResearchSettlement(admission.lease)
			return
		}
		h.settleResearch(r.Context(), admission.lease)
		return
	}
	body, readErr := io.ReadAll(admission.response.Body)
	if readErr != nil {
		h.deferResearchSettlement(admission.lease)
		http.Error(w, "read research response", http.StatusBadGateway)
		return
	}
	var status researchStatus
	if json.Unmarshal(body, &status) == nil && status.RequestID != "" {
		h.storeResearchMapping(status.RequestID, admission.lease, time.Now())
		if done, _ := researchTerminal(status.Status); done {
			h.settleResearch(r.Context(), admission.lease)
		}
	} else {
		h.deferResearchSettlement(admission.lease)
	}
	copyHeader(w.Header(), admission.response.Header)
	w.WriteHeader(admission.response.StatusCode)
	_, _ = w.Write(body)
}

func (h *Handler) settleResearch(ctx context.Context, lease pool.Lease) {
	h.pool.SettleResearch(lease)
	if h.usage == nil {
		return
	}
	if err := h.usage.RefreshUsage(ctx, lease.Key.Name); err != nil {
		slog.Warn("research usage reconciliation failed", "key", lease.Key.Name, "error", err)
	}
}

func (h *Handler) deferResearchSettlement(lease pool.Lease) {
	time.AfterFunc(h.researchTTL, func() {
		h.pool.SettleResearch(lease)
	})
}

func readUpstreamResponseError(response *http.Response) *upstreamResponseError {
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return &upstreamResponseError{
		status: response.StatusCode,
		header: response.Header.Clone(),
		body:   body,
	}
}

func (h *Handler) pollResearch(ctx context.Context, requestID, apiKey string) ([]byte, error) {
	response, err := h.researchRequest(ctx, http.MethodGet, "/research/"+requestID, apiKey, nil, nil)
	if err != nil {
		return nil, err
	}
	return readResearchResponse(response)
}

func (h *Handler) researchRequest(ctx context.Context, method, path, apiKey string, body []byte, headers http.Header) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build research request: %w", err)
	}
	copyHeader(request.Header, headers)
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
