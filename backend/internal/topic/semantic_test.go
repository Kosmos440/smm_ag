package topic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"smmagent/internal/model"
)

type cache struct {
	Store
	values map[string][]float32
}

func (c cache) Embedding(_ context.Context, model string, hash []byte) ([]float32, error) {
	return c.values[model+string(hash)], nil
}

func (c cache) SaveEmbedding(_ context.Context, model string, hash []byte, value []float32) error {
	c.values[model+string(hash)] = value
	return nil
}

func TestSemanticRoutesAndCaches(t *testing.T) {
	embeddingRequests := 0
	llmRequests := 0
	material := model.Material{Title: "Парламент избрал спикера",
		Body: "В ходе заседания депутаты утвердили нового председателя после обсуждения кандидатур и голосования."}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/embeddings":
			embeddingRequests++
			var request struct {
				Input []string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Input) != 2 {
				t.Errorf("expected both the article and reference on cache miss: %v, %v", request.Input, err)
			}
			fmt.Fprint(w, `{"data":[{"index":1,"embedding":[1,0]},{"index":0,"embedding":[1,0]}]}`)
		case "/chat":
			llmRequests++
			quote := "депутаты утвердили нового председателя"
			if llmRequests == 2 {
				quote = "несуществующая цитата"
			}
			answer, _ := json.Marshal(map[string]string{"verdict": "relevant", "subtopic": "Работа парламента", "quote": quote, "reason": "Избрание председателя"})
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(answer)}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := func(path, name string) *Provider {
		client := server.Client()
		client.Timeout = time.Second
		return &Provider{url: server.URL + path, model: name, client: client, limit: 5}
	}
	c := Classifier{Store: cache{values: map[string][]float32{}},
		Embeddings: provider("/embeddings", "multilingual-e5-base"), LLM: provider("/chat", "fixture")}
	profile := &model.TopicProfile{UseVectors: true, UseLLM: true, Subtopics: []model.Subtopic{{Name: "Работа парламента"}}}
	settings := model.Settings{Topic: "Политика", TopicProfile: profile}
	initial := model.TopicDecision{Verdict: model.TopicInsufficient}
	got := c.Resolve(t.Context(), material, settings, initial)
	if got.Verdict != model.TopicRelevant || got.Method != "llm" || got.Subtopic != "Работа парламента" || got.Score == nil {
		t.Fatalf("unexpected LLM result: %+v", got)
	}
	got = c.Resolve(t.Context(), material, settings, initial)
	if got.Verdict != model.TopicInsufficient || embeddingRequests != 1 || llmRequests != 2 {
		t.Fatalf("invalid citation or cache mishandled: %+v, embedding=%d, LLM=%d", got, embeddingRequests, llmRequests)
	}
	threshold := 0.9
	profile.VectorAccept = &threshold
	got = c.Resolve(t.Context(), material, settings, initial)
	if got.Verdict != model.TopicRelevant || got.Method != "vectors" || llmRequests != 2 {
		t.Fatalf("calibrated vector threshold did not resolve: %+v", got)
	}
}
