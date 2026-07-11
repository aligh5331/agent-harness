package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	goopenai "github.com/sashabaranov/go-openai"
)

// ---------------------------------------------------------------------------
// Retry-After capture via custom http.RoundTripper
// ---------------------------------------------------------------------------

// retryAfterCapture stores the Retry-After duration from the most recent HTTP
// 429 response. Thread-safe via mutex. Reset after each read.
type retryAfterCapture struct {
	mu         sync.Mutex
	retryAfter time.Duration
}

func (c *retryAfterCapture) set(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retryAfter = d
}

func (c *retryAfterCapture) getAndReset() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.retryAfter
	c.retryAfter = 0
	return d
}

// captureTransport wraps an http.RoundTripper to capture Retry-After headers
// from 429 responses before go-openai consumes the response.
type captureTransport struct {
	inner   http.RoundTripper
	capture *retryAfterCapture
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	if resp.StatusCode == 429 {
		ra := resp.Header.Get("Retry-After")
		if ra != "" {
			d, parseErr := parseHTTPRetryAfter(ra)
			if parseErr == nil {
				t.capture.set(d)
			}
		}
	}
	return resp, err
}

// parseHTTPRetryAfter parses a Retry-After header value.
// Supports both integer-seconds and HTTP-date (RFC1123) formats.
func parseHTTPRetryAfter(ra string) (time.Duration, error) {
	// Try integer seconds first (most common).
	if secs, err := strconv.Atoi(ra); err == nil {
		if secs < 0 {
			return 0, fmt.Errorf("negative retry-after: %d", secs)
		}
		return time.Duration(secs) * time.Second, nil
	}
	// Try HTTP-date format.
	if t, err := time.Parse(time.RFC1123, ra); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0, nil
		}
		return d, nil
	}
	return 0, fmt.Errorf("unparseable retry-after: %q", ra)
}

// ---------------------------------------------------------------------------
// OpenAIClient
// ---------------------------------------------------------------------------

// OpenAIClient is an LLM implementation backed by an OpenAI-compatible API endpoint.
// It wraps sashabaranov/go-openai internally and translates between our types
// and go-openai's types.
type OpenAIClient struct {
	capture *retryAfterCapture
}

// NewOpenAIClient creates a new OpenAIClient.
func NewOpenAIClient() *OpenAIClient {
	return &OpenAIClient{
		capture: &retryAfterCapture{},
	}
}

// Call sends a request to an OpenAI-compatible API endpoint and returns the response.
// The BaseURL field in the Request determines which endpoint is called.
func (c *OpenAIClient) Call(ctx context.Context, req Request) (Response, error) {
	config := goopenai.DefaultConfig(req.APIKey)
	config.BaseURL = req.BaseURL
	config.HTTPClient = &http.Client{
		Transport: &captureTransport{
			inner:   http.DefaultTransport,
			capture: c.capture,
		},
	}

	client := goopenai.NewClientWithConfig(config)

	openAIReq := goopenai.ChatCompletionRequest{
		Model:       req.Model,
		Messages:    convertMessages(req.Messages),
		MaxTokens:   req.MaxTokens,
		Temperature: float32(req.Temperature),
	}

	if len(req.Tools) > 0 {
		openAIReq.Tools = convertTools(req.Tools)
	}

	resp, err := client.CreateChatCompletion(ctx, openAIReq)
	if err != nil {
		return Response{}, c.classifyError(ctx, err)
	}

	return convertResponse(resp), nil
}

// classifyError translates a go-openai error into an *LLMError with a
// category that Phase 3's retry logic can act on.
func (c *OpenAIClient) classifyError(ctx context.Context, err error) error {
	// Check context errors first — they take precedence.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &LLMError{
			Err:      err,
			Category: ErrCategoryTimeout,
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return &LLMError{
			Err:      err,
			Category: ErrCategoryTimeout,
		}
	}

	// Unwrap to find the API error.
	var apiErr *goopenai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 429:
			body := strings.ToLower(apiErr.Message)
			retryAfter := c.capture.getAndReset()

			if strings.Contains(body, "quota") || strings.Contains(body, "insufficient_quota") {
				return &LLMError{
					Err:        err,
					Category:   ErrCategoryQuota,
					StatusCode: 429,
					RetryAfter: retryAfter,
				}
			}
			return &LLMError{
				Err:        err,
				Category:   ErrCategoryRateLimit,
				StatusCode: 429,
				RetryAfter: retryAfter,
			}

		case 401:
			// Unauthorized — bad API key, expired token. Never resolves by retrying.
			return &LLMError{
				Err:        err,
				Category:   ErrCategoryAuth,
				StatusCode: 401,
			}

		case 402:
			body := strings.ToLower(apiErr.Message)
			if strings.Contains(body, "quota") || strings.Contains(body, "insufficient_quota") || strings.Contains(body, "exceeded") {
				return &LLMError{
					Err:        err,
					Category:   ErrCategoryQuota,
					StatusCode: 402,
				}
			}
			return &LLMError{
				Err:        err,
				Category:   ErrCategoryAuth,
				StatusCode: 402,
			}

		case 403:
			body := strings.ToLower(apiErr.Message)
			if strings.Contains(body, "quota") || strings.Contains(body, "insufficient_quota") || strings.Contains(body, "exceeded") {
				return &LLMError{
					Err:        err,
					Category:   ErrCategoryQuota,
					StatusCode: 403,
				}
			}
			return &LLMError{
				Err:        err,
				Category:   ErrCategoryAuth,
				StatusCode: 403,
			}

		case 400:
			body := strings.ToLower(apiErr.Message)
			if strings.Contains(body, "quota") || strings.Contains(body, "insufficient_quota") || strings.Contains(body, "exceeded") {
				return &LLMError{
					Err:        err,
					Category:   ErrCategoryQuota,
					StatusCode: 400,
				}
			}
			return &LLMError{
				Err:        err,
				Category:   ErrCategoryUnknown,
				StatusCode: 400,
			}

		default:
			return &LLMError{
				Err:        err,
				Category:   ErrCategoryUnknown,
				StatusCode: apiErr.HTTPStatusCode,
			}
		}
	}

	// Request-level errors (network, DNS, TLS).
	var reqErr *goopenai.RequestError
	if errors.As(err, &reqErr) {
		return &LLMError{
			Err:      err,
			Category: ErrCategoryUnknown,
		}
	}

	// Fallback: uncategorized.
	return &LLMError{
		Err:      err,
		Category: ErrCategoryUnknown,
	}
}

func convertMessages(msgs []Message) []goopenai.ChatCompletionMessage {
	out := make([]goopenai.ChatCompletionMessage, len(msgs))
	for i, m := range msgs {
		out[i] = goopenai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.ToolCallID != "" {
			out[i].ToolCallID = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			out[i].ToolCalls = convertToolCalls(m.ToolCalls)
		}
	}
	return out
}

func convertTools(tools []ToolDef) []goopenai.Tool {
	out := make([]goopenai.Tool, len(tools))
	for i, t := range tools {
		out[i] = goopenai.Tool{
			Type: "function",
			Function: &goopenai.FunctionDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		}
	}
	return out
}

func convertToolCalls(calls []ToolCall) []goopenai.ToolCall {
	out := make([]goopenai.ToolCall, len(calls))
	for i, tc := range calls {
		out[i] = goopenai.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: goopenai.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		}
	}
	return out
}

func convertResponse(resp goopenai.ChatCompletionResponse) Response {
	r := Response{
		Usage: TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		r.Text = choice.Message.Content
		if len(choice.Message.ToolCalls) > 0 {
			r.ToolCalls = make([]ToolCall, len(choice.Message.ToolCalls))
			for i, tc := range choice.Message.ToolCalls {
				r.ToolCalls[i] = ToolCall{
					ID:   tc.ID,
					Function: ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
	}

	return r
}

// Ensure OpenAIClient satisfies LLM at compile time.
var _ LLM = (*OpenAIClient)(nil)
