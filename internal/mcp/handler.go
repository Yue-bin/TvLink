// Package mcp implements TvLink's authenticated MCP endpoint.
package mcp

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Handler implements a minimal Streamable HTTP MCP server.
type Handler struct {
	clientKey string
	version   string
	proxy     http.Handler
}

// New creates an MCP handler backed by a Tavily REST proxy.
func New(clientKey, version string, proxy http.Handler) *Handler {
	return &Handler{clientKey: clientKey, version: version, proxy: proxy}
}

// ServeHTTP serves MCP JSON-RPC requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+h.clientKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload request
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
		return
	}
	switch payload.Method {
	case "initialize":
		h.writeResult(w, payload.ID, map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "TvLink", "version": h.version},
		})
	case "tools/list":
		h.writeResult(w, payload.ID, map[string]any{"tools": tools()})
	case "tools/call":
		h.callTool(w, r, payload)
	default:
		h.writeError(w, payload.ID, -32601, "method not found")
	}
}

func (h *Handler) callTool(w http.ResponseWriter, r *http.Request, payload request) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      struct {
			ProgressToken any `json:"progressToken"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(payload.Params, &params); err != nil {
		h.writeError(w, payload.ID, -32602, "invalid tool parameters")
		return
	}
	path, ok := map[string]string{
		"tavily_search":   "/search",
		"tavily_extract":  "/extract",
		"tavily_crawl":    "/crawl",
		"tavily_map":      "/map",
		"tavily_research": "/research",
	}[params.Name]
	if !ok {
		h.writeError(w, payload.ID, -32602, "unknown tool")
		return
	}
	if params.Name == "tavily_research" {
		h.streamResearch(w, r, payload.ID, params.Arguments, params.Meta.ProgressToken)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, path, bytes.NewReader(params.Arguments))
	if err != nil {
		h.writeError(w, payload.ID, -32603, "build proxy request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+h.clientKey)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.proxy.ServeHTTP(response, req)
	body := response.Body.Bytes()
	result := map[string]any{
		"content": []map[string]string{{"type": "text", "text": response.Body.String()}},
		"isError": response.Code >= http.StatusBadRequest,
	}
	var structuredContent map[string]any
	if json.Unmarshal(body, &structuredContent) == nil && structuredContent != nil {
		result["structuredContent"] = structuredContent
	}
	h.writeResult(w, payload.ID, result)
}

type researchRunner interface {
	RunResearch(context.Context, []byte, func(string)) ([]byte, error)
}

func (h *Handler) streamResearch(w http.ResponseWriter, r *http.Request, id json.RawMessage, arguments json.RawMessage, progressToken any) {
	runner, ok := h.proxy.(researchRunner)
	if !ok {
		h.writeError(w, id, -32603, "research polling is unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.writeError(w, id, -32603, "research progress streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	writeAndFlush := func(message any) bool {
		if err := writeSSEMessage(w, message); err != nil {
			cancel()
			return false
		}
		flusher.Flush()
		return true
	}
	token, reportsProgress := validProgressToken(progressToken)
	progress := 0
	var streamErr error
	report := func(status string) {
		if !reportsProgress || streamErr != nil {
			return
		}
		progress++
		if !writeAndFlush(map[string]any{
			"jsonrpc": "2.0",
			"method":  "notifications/progress",
			"params": map[string]any{
				"progressToken": token,
				"progress":      progress,
				"message":       status,
			},
		}) {
			streamErr = ctx.Err()
		}
	}
	completed, err := runner.RunResearch(ctx, arguments, report)
	if streamErr != nil {
		return
	}
	if err != nil {
		writeAndFlush(errorMessage(id, -32603, err.Error()))
		return
	}
	result, err := researchResult(completed)
	if err != nil {
		writeAndFlush(errorMessage(id, -32603, err.Error()))
		return
	}
	writeAndFlush(resultMessage(id, result))
}

func (h *Handler) writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resultMessage(id, result)); err != nil {
		return
	}
}

func (h *Handler) writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(errorMessage(id, code, message)); err != nil {
		return
	}
}

func resultMessage(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
}

func errorMessage(id json.RawMessage, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": message}}
}

func writeSSEMessage(w http.ResponseWriter, message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode SSE message: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return fmt.Errorf("write SSE message: %w", err)
	}
	return nil
}

func validProgressToken(value any) (any, bool) {
	switch token := value.(type) {
	case string:
		return token, true
	case float64:
		return token, !math.IsInf(token, 0) && !math.IsNaN(token) && math.Trunc(token) == token
	default:
		return nil, false
	}
}

func researchText(body []byte) (string, error) {
	var completed struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(body, &completed); err != nil {
		return "", fmt.Errorf("decode completed research response: %w", err)
	}
	if len(completed.Content) == 0 {
		return "", fmt.Errorf("completed research response missing content")
	}
	var text string
	if err := json.Unmarshal(completed.Content, &text); err == nil {
		return text, nil
	}
	if !json.Valid(completed.Content) {
		return "", fmt.Errorf("decode completed research content")
	}
	return string(completed.Content), nil
}

func researchResult(body []byte) (map[string]any, error) {
	var structured map[string]any
	if err := json.Unmarshal(body, &structured); err != nil {
		return nil, fmt.Errorf("decode completed research response: %w", err)
	}
	if structured == nil {
		return nil, fmt.Errorf("completed research response is not an object")
	}
	text, err := researchText(body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": text}},
		"structuredContent": structured,
		"isError":           false,
	}, nil
}

//go:embed tavily_tools.json
var officialToolsJSON []byte

func tools() []map[string]any {
	var document struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(officialToolsJSON, &document); err != nil {
		panic("embedded Tavily tool schema is invalid: " + err.Error())
	}
	return document.Result.Tools
}
