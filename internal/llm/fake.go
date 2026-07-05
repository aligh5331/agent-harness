package llm

import (
	"context"
)

// Fake is an in-memory LLM implementation for tests.
// It returns a fixed Response and no error unless configured otherwise.
// Use the Responder field for per-call conditional behavior.
type Fake struct {
	// Response is returned when Responder is nil.
	Response Response
	// Err is returned when Responder is nil.
	Err error
	// Responder, if set, is called for every request, allowing per-call
	// inspection and conditional responses. When non-nil, Response and Err
	// are ignored.
	Responder func(ctx context.Context, req Request) (Response, error)

	// CallCount tracks how many times Call has been invoked.
	CallCount int
}

// Call implements LLM. It delegates to Responder if set; otherwise returns
// the configured Response and Err.
func (f *Fake) Call(ctx context.Context, req Request) (Response, error) {
	f.CallCount++
	if f.Responder != nil {
		return f.Responder(ctx, req)
	}
	return f.Response, f.Err
}

// Ensure Fake satisfies LLM at compile time.
var _ LLM = (*Fake)(nil)
