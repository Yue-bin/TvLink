# Research Credit Estimate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reserve a fixed, model-specific local credit estimate for Tavily Research requests.

**Architecture:** Keep `estimate` as the sole reservation entry point in the proxy. For `/research`, decode only the optional JSON `model` property and return one of three constants; all other paths retain their existing estimate behavior.

**Tech Stack:** Go standard library, `go test`.

---

### Task 1: Specify Research model estimates

**Files:**
- Modify: `D:\codes\TvLink\internal\proxy\handler_test.go`
- Test: `D:\codes\TvLink\internal\proxy\handler_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestEstimateUsesResearchModelDefaults(t *testing.T) {
	tests := []struct {
		name string
		body string
		want float64
	}{
		{name: "mini", body: `{"model":"mini"}`, want: 10},
		{name: "pro", body: `{"model":"pro"}`, want: 40},
		{name: "auto", body: `{"model":"auto"}`, want: 30},
		{name: "omitted", body: `{}`, want: 30},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := estimate("/research", []byte(test.body)); got != test.want {
				t.Fatalf("estimate() = %v, want %v", got, test.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `go test ./internal/proxy -run TestEstimateUsesResearchModelDefaults -count=1`

Expected: FAIL because the current function returns `1` for every Research body.

### Task 2: Decode the Research model in the estimator

**Files:**
- Modify: `D:\codes\TvLink\internal\proxy\handler.go:229`
- Test: `D:\codes\TvLink\internal\proxy\handler_test.go`

- [ ] **Step 1: Add the smallest model mapping**

```go
if path == "/research" {
	var request struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &request) == nil {
		switch request.Model {
		case "mini":
			return 10
		case "pro":
			return 40
		}
	}
	return 30
}
```

- [ ] **Step 2: Run the focused test to verify it passes**

Run: `go test ./internal/proxy -run TestEstimateUsesResearchModelDefaults -count=1`

Expected: PASS.

- [ ] **Step 3: Run the complete test suite and build**

Run: `go test ./... -count=1; go build ./cmd/tvlink`

Expected: both commands exit with status `0`.
