package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type recordedStreamHostCall struct {
	method string
	body   []byte
}

func recordStreamHostCalls(t *testing.T, emitErr error) *[]recordedStreamHostCall {
	t.Helper()
	old := streamHostCall
	calls := []recordedStreamHostCall{}
	streamHostCall = func(method string, body []byte) ([]byte, error) {
		calls = append(calls, recordedStreamHostCall{method: method, body: body})
		if method == pluginabi.MethodHostStreamEmit {
			return nil, emitErr
		}
		return nil, nil
	}
	t.Cleanup(func() { streamHostCall = old })
	return &calls
}

func streamCloseCallCount(calls []recordedStreamHostCall) int {
	count := 0
	for _, call := range calls {
		if call.method == pluginabi.MethodHostStreamClose {
			count++
		}
	}
	return count
}

func nativeStreamErrors(t *testing.T, calls []recordedStreamHostCall) []string {
	t.Helper()
	var messages []string
	for _, call := range calls {
		if call.method != pluginabi.MethodHostStreamEmit {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(call.body, &doc); err != nil {
			t.Fatalf("decode stream emit: %v", err)
		}
		if message, ok := doc["error"].(string); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

func setPumpHTTPClient(t *testing.T, body io.ReadCloser) {
	t.Helper()
	oldClient := sharedHTTPClient()
	sharedClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
			Request:    req,
		}, nil
	})}
	t.Cleanup(func() { sharedClient = oldClient })
}

func pumpTestRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://upstream.invalid/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestNativeStreamErrorWireUsesErrorNotPayload(t *testing.T) {
	raw := streamErrorWire("stream-1", "Bearer secret-token failed")
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, exists := doc["payload"]; exists {
		t.Fatalf("error encoded as payload: %#v", doc)
	}
	message, _ := doc["error"].(string)
	if message == "" || strings.Contains(message, "secret-token") {
		t.Fatalf("unredacted native error: %#v", doc)
	}
}

func TestTypedErrorEnvelopeCarriesHTTPStatus(t *testing.T) {
	raw := errorEnvelopeWithStatus("http_error", "upstream 429", http.StatusTooManyRequests)
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK || env.Error == nil || env.Error.HTTPStatus != http.StatusTooManyRequests || env.Error.Code != "http_error" {
		t.Fatalf("envelope = %#v", env)
	}
}

func TestStreamErrorAndCloseWireAreSeparate(t *testing.T) {
	errDoc := string(streamErrorWire("s", "failed"))
	closeDoc := string(streamCloseWire("s"))
	if !strings.Contains(errDoc, `"error":"failed"`) || strings.Contains(closeDoc, `"error"`) {
		t.Fatalf("error=%s close=%s", errDoc, closeDoc)
	}
}

func TestPumpUpstreamStreamEmptyUsesNativeErrorAndClosesOnce(t *testing.T) {
	calls := recordStreamHostCalls(t, nil)
	setPumpHTTPClient(t, io.NopCloser(strings.NewReader("")))

	pumpUpstreamStream(pumpTestRequest(t), nil, "stream-1", false, "model", "model", "", time.Now(), "", "")

	messages := nativeStreamErrors(t, *calls)
	if len(messages) != 1 || messages[0] != "empty upstream stream" {
		t.Fatalf("native errors = %#v", messages)
	}
	if closes := streamCloseCallCount(*calls); closes != 1 {
		t.Fatalf("stream closes = %d, want 1", closes)
	}
}

func TestPumpUpstreamStreamUsesCPAFrameContract(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"literal data: marker\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	for _, tc := range []struct {
		name      string
		sseFramed bool
		wantFrame bool
	}{
		{name: "native chat completions emits raw JSON"},
		{name: "cross-format keeps one SSE frame", sseFramed: true, wantFrame: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := recordStreamHostCalls(t, nil)
			setPumpHTTPClient(t, io.NopCloser(strings.NewReader(sse)))
			pumpUpstreamStream(pumpTestRequest(t), nil, "stream-1", tc.sseFramed, "model", "model", "", time.Now(), "", "")

			var chunks [][]byte
			for _, call := range *calls {
				if call.method != pluginabi.MethodHostStreamEmit {
					continue
				}
				var event struct {
					Payload []byte `json:"payload"`
					Error   string `json:"error"`
				}
				if err := json.Unmarshal(call.body, &event); err != nil {
					t.Fatal(err)
				}
				if event.Error != "" {
					t.Fatalf("unexpected stream error: %s", event.Error)
				}
				chunks = append(chunks, event.Payload)
			}
			if len(chunks) != 2 {
				t.Fatalf("expected two JSON events without upstream [DONE], got %q", chunks)
			}
			for i, payload := range chunks {
				if tc.wantFrame {
					if !strings.HasPrefix(string(payload), "data: ") {
						t.Fatalf("cross-format chunk %d lacks one SSE prefix: %q", i, payload)
					}
					payload = []byte(strings.TrimPrefix(string(payload), "data: "))
				} else if strings.HasPrefix(string(payload), "data:") {
					t.Fatalf("native chunk %d has an SSE prefix: %q", i, payload)
				}
				if !json.Valid(payload) {
					t.Fatalf("chunk %d is not JSON after the expected host framing: %q", i, payload)
				}
			}
			if !strings.Contains(string(chunks[0]), "literal data: marker") {
				t.Fatalf("JSON text containing data: was modified: %q", chunks[0])
			}
		})
	}
}

func TestPumpUpstreamStreamPreservesToolArgumentFragmentsAndUsage(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"terminal","arguments":"{\"command\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"printf HERMES_FIXTURE_OK\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}}`,
		`data: [DONE]`,
	}, "\n\n") + "\n\n"
	for _, tc := range []struct {
		name      string
		sseFramed bool
	}{
		{name: "native chat completions"},
		{name: "cross-format translator", sseFramed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := recordStreamHostCalls(t, nil)
			setPumpHTTPClient(t, io.NopCloser(strings.NewReader(sse)))
			pumpUpstreamStream(pumpTestRequest(t), nil, "stream-1", tc.sseFramed, "model", "model", "", time.Now(), "", "")

			var chunks [][]byte
			for _, call := range *calls {
				if call.method != pluginabi.MethodHostStreamEmit {
					continue
				}
				var event struct {
					Payload []byte `json:"payload"`
					Error   string `json:"error"`
				}
				if err := json.Unmarshal(call.body, &event); err != nil {
					t.Fatal(err)
				}
				if event.Error != "" {
					t.Fatalf("unexpected stream error: %s", event.Error)
				}
				chunks = append(chunks, event.Payload)
			}
			if len(chunks) != 4 {
				t.Fatalf("expected two tool deltas, finish, and usage, got %q", chunks)
			}
			var toolName, arguments string
			var promptTokens, completionTokens, totalTokens int
			for i, payload := range chunks {
				if tc.sseFramed {
					if !strings.HasPrefix(string(payload), "data: ") {
						t.Fatalf("cross-format chunk %d lacks one SSE prefix: %q", i, payload)
					}
					payload = []byte(strings.TrimPrefix(string(payload), "data: "))
				} else if strings.HasPrefix(string(payload), "data:") {
					t.Fatalf("native chunk %d has an SSE prefix: %q", i, payload)
				}
				if !json.Valid(payload) {
					t.Fatalf("chunk %d is not JSON after host framing: %q", i, payload)
				}
				var frame struct {
					Choices []struct {
						Delta struct {
							ToolCalls []struct {
								Function struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								} `json:"function"`
							} `json:"tool_calls"`
						} `json:"delta"`
					} `json:"choices"`
					Usage struct {
						PromptTokens     int `json:"prompt_tokens"`
						CompletionTokens int `json:"completion_tokens"`
						TotalTokens      int `json:"total_tokens"`
					} `json:"usage"`
				}
				if err := json.Unmarshal(payload, &frame); err != nil {
					t.Fatalf("decode chunk %d: %v", i, err)
				}
				for _, choice := range frame.Choices {
					for _, call := range choice.Delta.ToolCalls {
						toolName += call.Function.Name
						arguments += call.Function.Arguments
					}
				}
				if frame.Usage.TotalTokens != 0 {
					promptTokens, completionTokens, totalTokens = frame.Usage.PromptTokens, frame.Usage.CompletionTokens, frame.Usage.TotalTokens
				}
			}
			if toolName != "terminal" || arguments != `{"command":"printf HERMES_FIXTURE_OK"}` {
				t.Fatalf("tool fragments changed: name=%q arguments=%q", toolName, arguments)
			}
			if promptTokens != 11 || completionTokens != 4 || totalTokens != 15 {
				t.Fatalf("usage fields changed: prompt=%d completion=%d total=%d", promptTokens, completionTokens, totalTokens)
			}
		})
	}
}

func TestClientNeedsSSEFrameUsesRequestPathMetadata(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: "/v1/chat/completions", want: false},
		{path: "/v1/completions", want: false},
		{path: "/v1/responses", want: true},
		{path: "/v1/messages", want: true},
	} {
		if got := clientNeedsSSEFrame(map[string]any{"request_path": tc.path}); got != tc.want {
			t.Errorf("path %q needs SSE frame = %t, want %t", tc.path, got, tc.want)
		}
	}
}

type readErrorAfterData struct {
	data []byte
	err  error
}

func (r *readErrorAfterData) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	return 0, r.err
}

func (*readErrorAfterData) Close() error { return nil }

func TestPumpUpstreamStreamMidReadErrorUsesNativeErrorAndClosesOnce(t *testing.T) {
	calls := recordStreamHostCalls(t, nil)
	setPumpHTTPClient(t, &readErrorAfterData{
		data: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"),
		err:  io.ErrUnexpectedEOF,
	})

	pumpUpstreamStream(pumpTestRequest(t), nil, "stream-1", false, "model", "model", "", time.Now(), "", "")

	messages := nativeStreamErrors(t, *calls)
	if len(messages) != 1 || !strings.Contains(messages[0], "upstream stream read error") {
		t.Fatalf("native errors = %#v", messages)
	}
	if closes := streamCloseCallCount(*calls); closes != 1 {
		t.Fatalf("stream closes = %d, want 1", closes)
	}
}

func TestPumpUpstreamStreamEmitFailureDoesNotEmitSecondError(t *testing.T) {
	calls := recordStreamHostCalls(t, errors.New("client stream closed"))
	setPumpHTTPClient(t, io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")))

	canceled := false
	pumpUpstreamStream(pumpTestRequest(t), func() { canceled = true }, "stream-1", false, "model", "model", "", time.Now(), "", "")

	emits := 0
	for _, call := range *calls {
		if call.method == pluginabi.MethodHostStreamEmit {
			emits++
		}
	}
	if emits != 1 {
		t.Fatalf("stream emits = %d, want one failed chunk only", emits)
	}
	if closes := streamCloseCallCount(*calls); closes != 1 {
		t.Fatalf("stream closes = %d, want 1", closes)
	}
	if !canceled {
		t.Fatal("client stream closure did not cancel the upstream request")
	}
}

func TestSynchronousExecutorsReturnTypedHTTPStatus(t *testing.T) {
	const authID = "typed-http-status-ready"
	installModelStatesForTest(t, map[string]modelReadinessState{authID: modelReady})
	oldClient := sharedHTTPClient()
	sharedClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("Bearer status-secret-token exhausted")),
			Request:    req,
		}, nil
	})}
	t.Cleanup(func() { sharedClient = oldClient })

	executorReq := executorTestRequest()
	executorReq.AuthID = authID
	req := executorRequestWire{ExecutorRequest: executorReq}
	streamReq := executorStreamRequest{ExecutorRequest: executorReq}
	for name, invoke := range map[string]func([]byte) ([]byte, error){
		"execute":        handleExecExecute,
		"execute_stream": handleExecStream,
	} {
		t.Run(name, func(t *testing.T) {
			rawReq := mustJSON(req)
			if name == "execute_stream" {
				rawReq = mustJSON(streamReq)
			}
			raw, err := invoke(rawReq)
			if err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
			var env envelope
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatal(err)
			}
			if env.OK || env.Error == nil || env.Error.Code != "http_error" || env.Error.HTTPStatus != http.StatusTooManyRequests {
				t.Fatalf("envelope = %#v", env)
			}
			if strings.Contains(env.Error.Message, "status-secret-token") {
				t.Fatalf("unredacted message: %q", env.Error.Message)
			}
		})
	}
}

func executorTestRequest() pluginapi.ExecutorRequest {
	return pluginapi.ExecutorRequest{
		Model:       "test-model",
		Payload:     []byte(`{"messages":[]}`),
		StorageJSON: mustJSON(&storedAuth{Auth: storedTokens{AccessToken: "access-token"}}),
	}
}
