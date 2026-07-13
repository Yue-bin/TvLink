// Package mcp implements TvLink's authenticated MCP endpoint.
package mcp

import (
	"bytes"
	"encoding/json"
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
	proxy     http.Handler
}

// New creates an MCP handler backed by a Tavily REST proxy.
func New(clientKey string, proxy http.Handler) *Handler {
	return &Handler{clientKey: clientKey, proxy: proxy}
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
			"serverInfo":      map[string]string{"name": "TvLink", "version": "0.1.0"},
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
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, path, bytes.NewReader(params.Arguments))
	if err != nil {
		h.writeError(w, payload.ID, -32603, "build proxy request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+h.clientKey)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.proxy.ServeHTTP(response, req)
	h.writeResult(w, payload.ID, map[string]any{
		"content": []map[string]string{{"type": "text", "text": response.Body.String()}},
		"isError": response.Code >= http.StatusBadRequest,
	})
}

func (h *Handler) writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
}

func (h *Handler) writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": message}})
}

func tools() []map[string]any {
	return []map[string]any{
		{"name": "tavily_search", "description": "Search the web with Tavily.", "inputSchema": map[string]any{"type": "object"}},
		{"name": "tavily_extract", "description": "Extract web content with Tavily.", "inputSchema": map[string]any{"type": "object"}},
		{"name": "tavily_crawl", "description": "Crawl a site with Tavily.", "inputSchema": map[string]any{"type": "object"}},
		{"name": "tavily_map", "description": "Map a site with Tavily.", "inputSchema": map[string]any{"type": "object"}},
		{"name": "tavily_research", "description": "Create a Tavily research task.", "inputSchema": map[string]any{"type": "object"}},
	}
}
