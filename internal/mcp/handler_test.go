package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerListsToolsAfterAuthentication(t *testing.T) {
	handler := New("tlk-client", http.NotFoundHandler())
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
