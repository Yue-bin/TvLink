// Package tavily contains HTTP integration with the Tavily API.
package tavily

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tvlink/internal/pool"
)

type usageResponse struct {
	Key struct {
		Usage int64  `json:"usage"`
		Limit *int64 `json:"limit"`
	} `json:"key"`
	Account struct {
		PlanUsage  int64  `json:"plan_usage"`
		PlanLimit  *int64 `json:"plan_limit"`
		PaygoUsage int64  `json:"paygo_usage"`
		PaygoLimit *int64 `json:"paygo_limit"`
	} `json:"account"`
}

type retryAfterError struct {
	duration time.Duration
}

func (e retryAfterError) Error() string {
	return fmt.Sprintf("tavily rate limited request; retry after %s", e.duration)
}

// RetryAfter extracts Tavily's retry delay from a refresh error.
func RetryAfter(err error) (time.Duration, bool) {
	var rateLimit retryAfterError
	if errors.As(err, &rateLimit) {
		return rateLimit.duration, true
	}
	return 0, false
}

// Client fetches Tavily usage snapshots.
type Client struct {
	baseURL string
	http    *http.Client
	pool    *pool.Pool
	keys    map[string]pool.Key
}

// NewClient creates a Tavily usage client.
func NewClient(baseURL string, httpClient *http.Client, keyPool *pool.Pool, keys []pool.Key) *Client {
	configured := make(map[string]pool.Key, len(keys))
	for _, key := range keys {
		configured[key.Name] = key
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    httpClient,
		pool:    keyPool,
		keys:    configured,
	}
}

// RefreshUsage refreshes one key's authoritative Tavily usage snapshot.
func (c *Client) RefreshUsage(ctx context.Context, name string) error {
	key, ok := c.keys[name]
	if !ok {
		return fmt.Errorf("unknown Tavily key %q", name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/usage", nil)
	if err != nil {
		return fmt.Errorf("build usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key.APIKey)

	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send usage request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return retryAfterError{duration: parseRetryAfter(response.Header.Get("Retry-After"), time.Now())}
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("usage request returned %s", response.Status)
	}

	var payload usageResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode usage response: %w", err)
	}
	limit, used, err := effectiveUsage(payload)
	if err != nil {
		return fmt.Errorf("usage response for %q: %w", name, err)
	}
	c.pool.UpdateUsage(name, pool.Usage{Limit: limit, Used: used}, time.Now())
	return nil
}

func effectiveUsage(payload usageResponse) (int64, int64, error) {
	if payload.Account.PlanLimit == nil {
		return 0, 0, fmt.Errorf("account plan_limit is missing")
	}
	accountLimit := *payload.Account.PlanLimit
	if payload.Account.PaygoLimit != nil {
		accountLimit += *payload.Account.PaygoLimit
	}
	accountUsage := payload.Account.PlanUsage + payload.Account.PaygoUsage
	if payload.Key.Limit == nil {
		return accountLimit, accountUsage, nil
	}
	keyRemaining := max(0, *payload.Key.Limit-payload.Key.Usage)
	accountRemaining := max(0, accountLimit-accountUsage)
	return min(keyRemaining, accountRemaining), 0, nil
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	seconds, err := strconv.Atoi(value)
	if err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		return time.Until(retryAt)
	}
	return time.Minute
}
