package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestEnvThinkingAndReasoningEffortReachChatCompletions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path=%s", r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Thinking struct {
				Type string `json:"type"`
			} `json:"thinking"`
			Effort string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Thinking.Type != "enabled" || body.Effort != "low" || body.Model != "deepseek/deepseek-v4.1-flash" {
			t.Errorf("wire configuration: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer srv.Close()
	for key, value := range map[string]string{
		"ZEROX_LLM_PROVIDER": "openai", "OPENAI_API_KEY": "local-test-not-a-secret",
		"ZEROX_LLM_BASE_URL": srv.URL + "/v1", "ZEROX_LLM_MODEL": "deepseek/deepseek-v4.1-flash",
		"ZEROX_LLM_THINKING_TYPE": "enabled", "ZEROX_LLM_REASONING_EFFORT": "low",
	} {
		t.Setenv(key, value)
	}
	cfg, ok := FromEnv()
	if !ok {
		t.Fatal("environment not resolved")
	}
	provider, err := cfg.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = provider.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("Reply OK")}})
	if err != nil {
		t.Fatal(err)
	}
}
