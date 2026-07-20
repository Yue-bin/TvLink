package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeResearchRunner struct {
	result   []byte
	err      error
	statuses []string
}

func (f fakeResearchRunner) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	http.NotFound(w, nil)
}

func (f fakeResearchRunner) RunResearch(_ context.Context, _ []byte, progress func(string)) ([]byte, error) {
	statuses := f.statuses
	if len(statuses) == 0 {
		statuses = []string{"pending", "in_progress"}
	}
	for _, status := range statuses {
		progress(status)
	}
	return f.result, f.err
}

func TestHandlerListsToolsAfterAuthentication(t *testing.T) {
	handler := New("tlk-client", "1.2.3", http.NotFoundHandler())
	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	request := httptest.NewRequest(http.MethodPost, "/mcp", body)
	request.Header.Set("Authorization", "Bearer tlk-client")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["result"] == nil {
		t.Fatalf("result is missing: %s", response.Body.String())
	}
}

func TestInitializeReportsConfiguredVersion(t *testing.T) {
	handler := New("tlk-client", "1.2.3", http.NotFoundHandler())
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	request.Header.Set("Authorization", "Bearer tlk-client")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	var payload struct {
		Result struct {
			ServerInfo struct {
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Result.ServerInfo.Version != "1.2.3" {
		t.Errorf("serverInfo.version = %q, want %q", payload.Result.ServerInfo.Version, "1.2.3")
	}
}

func TestToolsExposeRequiredResearchInputSchema(t *testing.T) {
	var research map[string]any
	for _, tool := range tools() {
		if tool["name"] == "tavily_research" {
			research = tool
			break
		}
	}
	if research == nil {
		t.Fatal("tavily_research is missing")
	}
	schema := research["inputSchema"].(map[string]any)
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("research input schema does not define properties")
	}
	if _, ok := properties["input"]; !ok {
		t.Fatal("research input schema does not define input")
	}
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("research input schema does not define required")
	}
	if len(required) != 1 || required[0] != "input" {
		t.Errorf("research required = %#v, want [input]", required)
	}
	if schema["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", schema["additionalProperties"])
	}
}

func TestCallToolReturnsStructuredContentForJSONResponse(t *testing.T) {
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("path = %q, want /search", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"TvLink","results":[{"title":"result"}]}`))
	})
	handler := New("tlk-client", "1.2.3", proxy)
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tavily_search","arguments":{"query":"TvLink"}}}`))
	request.Header.Set("Authorization", "Bearer tlk-client")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	var payload struct {
		Result struct {
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Result.StructuredContent["query"] != "TvLink" {
		t.Errorf("structuredContent.query = %#v, want TvLink", payload.Result.StructuredContent["query"])
	}
}

func TestResearchReportsProgressOverSSE(t *testing.T) {
	runner := fakeResearchRunner{
		result:   []byte(`{"request_id":"research-1","created_at":"2026-07-21T00:00:00Z","status":"completed","content":"report","sources":[],"response_time":1.2}`),
		statuses: []string{"pending", "pending", "in_progress", "in_progress"},
	}
	handler := New("tlk-client", "1.2.3", runner)
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"tavily_research","arguments":{"input":"test"},"_meta":{"progressToken":"research-progress"}}}`))
	request.Header.Set("Authorization", "Bearer tlk-client")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	messages := decodeSSEMessages(t, response.Body.String())
	if len(messages) != 4 {
		t.Fatalf("SSE messages = %d, want 4: %s", len(messages), response.Body.String())
	}
	wantProgress := []struct {
		progress float64
		message  string
	}{
		{progress: 1, message: "pending"},
		{progress: 2, message: "in_progress"},
		{progress: 3, message: "completed"},
	}
	for index, want := range wantProgress {
		message := messages[index]
		if message["method"] != "notifications/progress" {
			t.Errorf("message %d method = %#v", index, message["method"])
		}
		params := message["params"].(map[string]any)
		if params["progressToken"] != "research-progress" || params["progress"] != want.progress || params["total"] != float64(3) || params["message"] != want.message {
			t.Errorf("message %d params = %#v", index, params)
		}
	}
	if messages[3]["id"] != float64(7) {
		t.Errorf("final id = %#v, want 7", messages[3]["id"])
	}
	result := messages[3]["result"].(map[string]any)
	content := result["content"].([]any)[0].(map[string]any)
	if content["text"] != "report" {
		t.Errorf("final text = %#v, want report", content["text"])
	}
}

func TestResearchReturnsErrorOverSSE(t *testing.T) {
	runner := fakeResearchRunner{err: errors.New("research unavailable")}
	handler := New("tlk-client", "1.2.3", runner)
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"tavily_research","arguments":{"input":"test"}}}`))
	request.Header.Set("Authorization", "Bearer tlk-client")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	messages := decodeSSEMessages(t, response.Body.String())
	if len(messages) != 1 {
		t.Fatalf("SSE messages = %d, want 1: %s", len(messages), response.Body.String())
	}
	errorPayload := messages[0]["error"].(map[string]any)
	if errorPayload["message"] != "research unavailable" {
		t.Errorf("error message = %#v", errorPayload["message"])
	}
}

func TestResearchReturnsStructuredContent(t *testing.T) {
	runner := fakeResearchRunner{result: []byte(`{"request_id":"research-1","created_at":"2026-07-21T00:00:00Z","status":"completed","content":"report","sources":[{"title":"source","url":"https://example.com","favicon":"https://example.com/favicon.ico"}],"response_time":1.2}`)}
	result := callResearchResult(t, runner, 9)

	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %#v, want object", result["structuredContent"])
	}
	if structured["request_id"] != "research-1" || structured["status"] != "completed" || structured["content"] != "report" {
		t.Errorf("structuredContent = %#v", structured)
	}
	content := result["content"].([]any)[0].(map[string]any)
	if content["text"] != "report" {
		t.Errorf("text = %#v, want report", content["text"])
	}
}

func TestResearchFormatsObjectContentAsText(t *testing.T) {
	runner := fakeResearchRunner{result: []byte(`{"request_id":"research-2","created_at":"2026-07-21T00:00:00Z","status":"completed","content":{"summary":"report"},"sources":[],"response_time":1.2}`)}
	result := callResearchResult(t, runner, 10)

	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %#v, want object", result["structuredContent"])
	}
	structuredContent := structured["content"].(map[string]any)
	if structuredContent["summary"] != "report" {
		t.Errorf("structured content = %#v", structuredContent)
	}
	content := result["content"].([]any)[0].(map[string]any)
	if content["text"] != `{"summary":"report"}` {
		t.Errorf("text = %#v, want compact JSON", content["text"])
	}
}

func callResearchResult(t *testing.T, runner fakeResearchRunner, id int) map[string]any {
	t.Helper()
	handler := New("tlk-client", "1.2.3", runner)
	requestBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "tavily_research",
			"arguments": map[string]any{"input": "test"},
		},
	})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer tlk-client")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	messages := decodeSSEMessages(t, response.Body.String())
	if len(messages) != 1 {
		t.Fatalf("SSE messages = %d, want 1: %s", len(messages), response.Body.String())
	}
	result, ok := messages[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want object", messages[0]["result"])
	}
	return result
}

func decodeSSEMessages(t *testing.T, body string) []map[string]any {
	t.Helper()
	var messages []map[string]any
	for _, event := range strings.Split(strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n"), "\n\n") {
		if !strings.HasPrefix(event, "data: ") {
			t.Fatalf("invalid SSE event %q", event)
		}
		var message map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(event, "data: ")), &message); err != nil {
			t.Fatalf("decode SSE event: %v", err)
		}
		messages = append(messages, message)
	}
	return messages
}
