package model

import "context"

// NativeObservation is response-only telemetry. It is never replayed as history.
type NativeObservation struct {
	Model               string `json:"model,omitempty"`
	Purpose             string `json:"purpose"`
	Done                bool   `json:"done"`
	DoneReason          string `json:"done_reason,omitempty"`
	Thinking            string `json:"thinking,omitempty"`
	Content             string `json:"content,omitempty"`
	ToolCalls           int    `json:"tool_calls"`
	PromptEvalCount     int    `json:"prompt_eval_count"`
	EvalCount           int    `json:"eval_count"`
	TotalDuration       int64  `json:"total_duration_ns"`
	LoadDuration        int64  `json:"load_duration_ns"`
	PromptEvalDuration  int64  `json:"prompt_eval_duration_ns"`
	EvalDuration        int64  `json:"eval_duration_ns"`
	RequestedNumCtx     *int   `json:"requested_num_ctx,omitempty"`
	RequestedNumPredict *int   `json:"requested_num_predict,omitempty"`
	HTTPStatus          int    `json:"http_status"`
	Error               string `json:"error,omitempty"`
}

type nativeObserverKey struct{}
type modelPurposeKey struct{}

// WithNativeObserver scopes logging to a turn, not a shared mutable client.
func WithNativeObserver(ctx context.Context, sink func(NativeObservation)) context.Context {
	return context.WithValue(ctx, nativeObserverKey{}, sink)
}
func WithModelPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, modelPurposeKey{}, purpose)
}
func modelPurpose(ctx context.Context) string {
	if purpose, ok := ctx.Value(modelPurposeKey{}).(string); ok {
		return purpose
	}
	return "unspecified"
}
func observeNative(ctx context.Context, o NativeObservation) {
	if sink, ok := ctx.Value(nativeObserverKey{}).(func(NativeObservation)); ok && sink != nil {
		sink(o)
	}
}
