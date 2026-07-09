package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// compileTimeCheck ensures Fake satisfies LLM at compile time.
var _ LLM = (*Fake)(nil)

func TestFake_DefaultBehavior(t *testing.T) {
	f := &Fake{}
	ctx := context.Background()

	resp, err := f.Call(ctx, Request{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Text != "" {
		t.Errorf("expected empty Text, got %q", resp.Text)
	}
	if resp.Usage.TotalTokens != 0 {
		t.Errorf("expected zero token usage, got %d", resp.Usage.TotalTokens)
	}
}

func TestFake_FixedResponse(t *testing.T) {
	expectedResp := Response{
		Text: "hello world",
		Usage: TokenUsage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}
	f := &Fake{Response: expectedResp}
	ctx := context.Background()

	resp, err := f.Call(ctx, Request{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Text != expectedResp.Text {
		t.Errorf("Text: got %q, want %q", resp.Text, expectedResp.Text)
	}
	if resp.Usage.TotalTokens != expectedResp.Usage.TotalTokens {
		t.Errorf("TotalTokens: got %d, want %d", resp.Usage.TotalTokens, expectedResp.Usage.TotalTokens)
	}
}

func TestFake_FixedError(t *testing.T) {
	expectedErr := errors.New("test error")
	f := &Fake{Err: expectedErr}
	ctx := context.Background()

	_, err := f.Call(ctx, Request{})
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestFake_Responder(t *testing.T) {
	var gotReq Request
	f := &Fake{
		Responder: func(ctx context.Context, req Request) (Response, error) {
			gotReq = req
			return Response{Text: "from responder"}, nil
		},
	}
	ctx := context.Background()

	req := Request{
		Model:   "test-model",
		BaseURL: "https://example.com/v1",
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
	}

	resp, err := f.Call(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Text != "from responder" {
		t.Errorf("Text: got %q, want %q", resp.Text, "from responder")
	}
	if gotReq.Model != req.Model {
		t.Errorf("Model: got %q, want %q", gotReq.Model, req.Model)
	}
}

func TestFake_CallCount(t *testing.T) {
	f := &Fake{Response: Response{Text: "ok"}}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, _ = f.Call(ctx, Request{})
	}
	if f.CallCount != 3 {
		t.Errorf("expected CallCount=3, got %d", f.CallCount)
	}
}

func TestFake_ResponderTakesPrecedence(t *testing.T) {
	f := &Fake{
		Response:  Response{Text: "fixed"},
		Err:       errors.New("fixed error"),
		Responder: func(ctx context.Context, req Request) (Response, error) {
			return Response{Text: "responder"}, nil
		},
	}
	ctx := context.Background()

	resp, err := f.Call(ctx, Request{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Text != "responder" {
		t.Errorf("expected responder text, got %q", resp.Text)
	}
}

// TestLLMError_ImplementsError ensures LLMError satisfies the error interface
// and unwraps correctly for errors.As inspection.
func TestLLMError_ImplementsError(t *testing.T) {
	inner := errors.New("underlying error")
	llmErr := &LLMError{
		Err:      inner,
		Category: ErrCategoryTimeout,
	}

	if llmErr.Error() == "" {
		t.Error("expected non-empty Error() string")
	}

	// errors.Is should traverse the chain to the inner error.
	if !errors.Is(llmErr, inner) {
		t.Error("errors.Is should match the wrapped error")
	}

	// errors.As should match *LLMError.
	var matched *LLMError
	if !errors.As(llmErr, &matched) {
		t.Error("errors.As should match *LLMError")
	}
	if matched.Category != ErrCategoryTimeout {
		t.Errorf("Category: got %d, want %d", matched.Category, ErrCategoryTimeout)
	}
}

func TestLLMError_Categories(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		category ErrorCategory
	}{
		{
			name:     "timeout",
			err:      &LLMError{Err: errors.New("timeout"), Category: ErrCategoryTimeout},
			category: ErrCategoryTimeout,
		},
		{
			name:     "rate_limit",
			err:      &LLMError{Err: errors.New("rate limit"), Category: ErrCategoryRateLimit, RetryAfter: 30},
			category: ErrCategoryRateLimit,
		},
		{
			name:     "quota",
			err:      &LLMError{Err: errors.New("quota"), Category: ErrCategoryQuota},
			category: ErrCategoryQuota,
		},
		{
			name:     "malformed",
			err:      &LLMError{Err: errors.New("malformed"), Category: ErrCategoryMalformed},
			category: ErrCategoryMalformed,
		},
		{
			name:     "unknown",
			err:      &LLMError{Err: errors.New("unknown"), Category: ErrCategoryUnknown},
			category: ErrCategoryUnknown,
		},
		{
			name:     "auth",
			err:      &LLMError{Err: errors.New("auth"), Category: ErrCategoryAuth},
			category: ErrCategoryAuth,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var matched *LLMError
			if !errors.As(tt.err, &matched) {
				t.Fatal("should match *LLMError")
			}
			if matched.Category != tt.category {
				t.Errorf("Category: got %d, want %d", matched.Category, tt.category)
			}
		})
	}
}

func TestOpenAIClient_Compiles(t *testing.T) {
	// Verify that OpenAIClient satisfies the LLM interface at compile time.
	var _ LLM = (*OpenAIClient)(nil)
}

func TestTokenUsage(t *testing.T) {
	u := TokenUsage{
		PromptTokens:     10,
		CompletionTokens: 20,
		TotalTokens:      30,
	}
	if u.PromptTokens != 10 {
		t.Errorf("PromptTokens: got %d, want %d", u.PromptTokens, 10)
	}
	if u.CompletionTokens != 20 {
		t.Errorf("CompletionTokens: got %d, want %d", u.CompletionTokens, 20)
	}
	if u.TotalTokens != 30 {
		t.Errorf("TotalTokens: got %d, want %d", u.TotalTokens, 30)
	}
}

func TestMessageRoles(t *testing.T) {
	// Verify all four roles are representable with the Message struct.
	sys := Message{Role: "system", Content: "You are a helpful assistant."}
	user := Message{Role: "user", Content: "Hello"}
	assistant := Message{Role: "assistant", Content: "Hi!", ToolCalls: []ToolCall{
		{ID: "call_1", Function: ToolCallFunction{Name: "read_file", Arguments: `{"path":"x.go"}`}},
	}}
	tool := Message{Role: "tool", Content: `{"lines":42}`, ToolCallID: "call_1"}

	if sys.Role != "system" {
		t.Errorf("expected system role")
	}
	if user.Role != "user" {
		t.Errorf("expected user role")
	}
	if assistant.Role != "assistant" || len(assistant.ToolCalls) != 1 {
		t.Errorf("expected assistant with 1 tool call")
	}
	if tool.Role != "tool" || tool.ToolCallID != "call_1" {
		t.Errorf("expected tool role with tool_call_id")
	}
}

func TestToolDefAndCall(t *testing.T) {
	td := ToolDef{
		Function: ToolFunction{
			Name:        "read_file",
			Description: "Read a file from disk",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
	}
	if td.Function.Name != "read_file" {
		t.Errorf("ToolDef name: got %q, want %q", td.Function.Name, "read_file")
	}

	tc := ToolCall{
		ID: "call_abc123",
		Function: ToolCallFunction{
			Name:      "read_file",
			Arguments: `{"path":"main.go"}`,
		},
	}
	if tc.ID != "call_abc123" {
		t.Errorf("ToolCall ID: got %q, want %q", tc.ID, "call_abc123")
	}
	if tc.Function.Arguments != `{"path":"main.go"}` {
		t.Errorf("ToolCall arguments mismatch")
	}
}

// ---------------------------------------------------------------------------
// Authorization header verification test
// ---------------------------------------------------------------------------

func TestOpenAIClient_SendsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"test","object":"chat.completion","created":123,"model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
	}))
	defer srv.Close()

	client := NewOpenAIClient()
	resp, err := client.Call(context.Background(), Request{
		Model:   "test-model",
		BaseURL: srv.URL + "/v1",
		APIKey:  "sk-test-key-12345",
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("Text = %q, want %q", resp.Text, "ok")
	}
	expectedAuth := "Bearer sk-test-key-12345"
	if gotAuth != expectedAuth {
		t.Errorf("Authorization header = %q, want %q", gotAuth, expectedAuth)
	}
}


