// Package mcp implements TvLink's authenticated MCP endpoint.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	h.writeResult(w, payload.ID, map[string]any{
		"content": []map[string]string{{"type": "text", "text": response.Body.String()}},
		"isError": response.Code >= http.StatusBadRequest,
	})
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
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	var content strings.Builder
	var sources any
	scanner := bufio.NewScanner(response.Body)
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
		if delta.Sources != nil {
			sources = delta.Sources
		}
		if delta.ToolCalls != nil && progressToken != nil {
			h.writeSSE(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{"progressToken": progressToken, "progress": 0, "message": "Tavily research is in progress"}})
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		h.writeSSE(w, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": -32603, "message": fmt.Sprintf("read research stream: %v", err)}})
		return
	}
	h.writeSSE(w, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": map[string]any{"content": []map[string]any{{"type": "text", "text": content.String()}, {"type": "resource", "resource": map[string]any{"sources": sources}}}}})
}

func (h *Handler) writeSSE(w io.Writer, payload any) {
	encoded, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
}

func (h *Handler) writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
}

func (h *Handler) writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": message}})
}

func tools() []map[string]any {
	return []map[string]any{
		{"name": "tavily_search", "description": "Search the web for current information. Returns ranked snippets and source URLs.", "inputSchema": objectSchema(map[string]any{
			"query": stringSchema("The search query."), "max_results": integerSchema("Maximum results to return.", 5),
			"search_depth":   enumSchema("Search relevance and cost tier.", "basic", "basic", "advanced", "fast", "ultra-fast"),
			"topic":          enumSchema("Search topic.", "general", "general", "news", "finance"),
			"time_range":     enumSchema("Optional recency filter.", "", "day", "week", "month", "year"),
			"include_answer": boolSchema("Include a generated answer.", false), "include_raw_content": boolSchema("Include cleaned source content.", false),
			"include_images": boolSchema("Include related images.", false), "include_image_descriptions": boolSchema("Describe included images.", false),
			"include_favicon": boolSchema("Include source favicon URLs.", false), "include_domains": stringArraySchema("Only search these domains."),
			"exclude_domains": stringArraySchema("Exclude these domains."), "country": stringSchema("Optional full country name for general searches."),
		}, "query")},
		{"name": "tavily_extract", "description": "Extract clean markdown or text from one or more known URLs.", "inputSchema": objectSchema(map[string]any{
			"urls": stringArraySchema("URLs to extract."), "extract_depth": enumSchema("Extraction depth.", "basic", "basic", "advanced"),
			"format": enumSchema("Returned content format.", "markdown", "markdown", "text"), "include_images": boolSchema("Include page images.", false),
			"include_favicon": boolSchema("Include favicon URLs.", false), "query": stringSchema("Optional query for relevance ranking."),
		}, "urls")},
		{"name": "tavily_crawl", "description": "Crawl a website from a root URL and extract discovered pages.", "inputSchema": objectSchema(siteTraversalProperties(true), "url")},
		{"name": "tavily_map", "description": "Discover a website's URL structure without extracting page content.", "inputSchema": objectSchema(siteTraversalProperties(false), "url")},
		{"name": "tavily_research", "description": "Perform comprehensive multi-source research. Returns the completed cited report; TvLink streams progress when the client supports it.", "inputSchema": objectSchema(map[string]any{
			"input":           stringSchema("A complete research question or task. This field is required; do not use query."),
			"model":           enumSchema("Research breadth. mini suits narrow tasks; pro suits broad multi-angle tasks.", "auto", "mini", "pro", "auto"),
			"output_length":   enumSchema("Target report size.", "standard", "short", "standard", "long"),
			"citation_format": enumSchema("Citation style.", "numbered", "numbered", "mla", "apa", "chicago"),
			"include_domains": stringArraySchema("Preferred source domains."), "exclude_domains": stringArraySchema("Blocked source domains."),
			"output_schema": map[string]any{"type": "object", "description": "Optional JSON Schema for structured report content."},
		}, "input")},
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func boolSchema(description string, defaultValue bool) map[string]any {
	return map[string]any{"type": "boolean", "description": description, "default": defaultValue}
}
func integerSchema(description string, defaultValue int) map[string]any {
	return map[string]any{"type": "integer", "description": description, "default": defaultValue, "minimum": 1}
}
func stringArraySchema(description string) map[string]any {
	return map[string]any{"type": "array", "description": description, "items": map[string]any{"type": "string"}, "default": []string{}}
}
func enumSchema(description, defaultValue string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "default": defaultValue, "enum": values}
}

func siteTraversalProperties(includeExtraction bool) map[string]any {
	properties := map[string]any{
		"url": stringSchema("The root URL."), "max_depth": integerSchema("Maximum link depth.", 1), "max_breadth": integerSchema("Maximum links followed per page.", 20),
		"limit": integerSchema("Maximum URLs to process.", 50), "instructions": stringSchema("Optional natural-language page selection instructions."),
		"select_paths": stringArraySchema("Regular expressions selecting URL paths."), "select_domains": stringArraySchema("Regular expressions selecting domains."),
		"allow_external": boolSchema("Allow external links in results.", true),
	}
	if includeExtraction {
		properties["extract_depth"] = enumSchema("Extraction depth.", "basic", "basic", "advanced")
		properties["format"] = enumSchema("Extracted content format.", "markdown", "markdown", "text")
		properties["include_favicon"] = boolSchema("Include favicon URLs.", false)
	}
	return properties
}
