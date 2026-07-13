// Package mcp implements TvLink's authenticated MCP endpoint.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	w.Header().Set("Content-Type", "application/json")
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

type researchStreamer interface {
	StreamResearch(context.Context, []byte) (*http.Response, error)
}

func (h *Handler) streamResearch(w http.ResponseWriter, r *http.Request, id json.RawMessage, arguments json.RawMessage, progressToken any) {
	streamer, ok := h.proxy.(researchStreamer)
	if !ok {
		h.writeError(w, id, -32603, "research streaming is unavailable")
		return
	}
	response, err := streamer.StreamResearch(r.Context(), arguments)
	if err != nil {
		h.writeError(w, id, -32603, err.Error())
		return
	}
	defer response.Body.Close()
	var content strings.Builder
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var event struct {
			Choices []struct {
				Delta struct {
					Content   json.RawMessage `json:"content"`
					Sources   any             `json:"sources"`
					ToolCalls any             `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &event) != nil || len(event.Choices) == 0 {
			continue
		}
		delta := event.Choices[0].Delta
		if len(delta.Content) > 0 {
			var text string
			if json.Unmarshal(delta.Content, &text) == nil {
				content.WriteString(text)
			} else {
				content.Write(delta.Content)
			}
		}
		_ = delta.Sources
		_ = delta.ToolCalls
		_ = progressToken
	}
	if err := scanner.Err(); err != nil {
		h.writeError(w, id, -32603, fmt.Sprintf("read research stream: %v", err))
		return
	}
	h.writeResult(w, id, map[string]any{"content": []map[string]string{{"type": "text", "text": content.String()}}})
}

func (h *Handler) writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
}

func (h *Handler) writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": message}})
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
