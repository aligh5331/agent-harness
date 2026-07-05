package llm

import (
	"context"
	"errors"
	"strings"
	"time"

	goopenai "github.com/sashabaranov/go-openai"
)

// OpenAIClient is an LLM implementation backed by an OpenAI-compatible API endpoint.
// It wraps sashabaranov/go-openai internally and translates between our types
// and go-openai's types.
type OpenAIClient struct{}

// NewOpenAIClient creates a new OpenAIClient.
func NewOpenAIClient() *OpenAIClient {
	return &OpenAIClient{}
}

// Call sends a request to an OpenAI-compatible API endpoint and returns the response.
// The BaseURL field in the Request determines which endpoint is called.
func (c *OpenAIClient) Call(ctx context.Context, req Request) (Response, error) {
	config := goopenai.DefaultConfig(req.Model)
	config.BaseURL = req.BaseURL

	client := goopenai.NewClientWithConfig(config)

	openAIReq := goopenai.ChatCompletionRequest{
		Model:       req.Model,
		Messages:    convertMessages(req.Messages),
		MaxTokens:   req.MaxTokens,
		Temperature: 0, // not configurable yet — Phase 4 adds per-agent config
	}

	if len(req.Tools) > 0 {
		openAIReq.Tools = convertTools(req.Tools)
	}

	resp, err := client.CreateChatCompletion(ctx, openAIReq)
	if err != nil {
		return Response{}, classifyError(ctx, err)
	}

	return convertResponse(resp), nil
}

// classifyError translates a go-openai error into an *LLMError with a
// category that Phase 3's retry logic can act on.
func classifyError(ctx context.Context, err error) error {
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
			// Rate limit or quota exhausted — check the body for clues.
			body := strings.ToLower(apiErr.Message)
			retryAfter := parseRetryAfter(apiErr)

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

		case 400, 401, 402, 403:
			body := strings.ToLower(apiErr.Message)
			if strings.Contains(body, "quota") || strings.Contains(body, "insufficient_quota") || strings.Contains(body, "exceeded") {
				return &LLMError{
					Err:        err,
					Category:   ErrCategoryQuota,
					StatusCode: apiErr.HTTPStatusCode,
				}
			}
			fallthrough

		default:
			return &LLMError{
				Err:        err,
				Category:   ErrCategoryOther,
				StatusCode: apiErr.HTTPStatusCode,
			}
		}
	}

	// Request-level errors (network, DNS, TLS).
	var reqErr *goopenai.RequestError
	if errors.As(err, &reqErr) {
		return &LLMError{
			Err:      err,
			Category: ErrCategoryOther,
		}
	}

	// Fallback: uncategorized.
	return &LLMError{
		Err:      err,
		Category: ErrCategoryOther,
	}
}

// parseRetryAfter attempts to extract the Retry-After value from a go-openai APIError.
func parseRetryAfter(apiErr *goopenai.APIError) time.Duration {
	// go-openai does not expose individual response headers directly, but the
	// APIError may contain the Retry-After value in its message or as a separate field.
	// For now we return 0 and let the caller apply its own default backoff.
	// In a future iteration we can inspect apiErr.RetryAfter if go-openai exposes it.
	return 0
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
