package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
