package topic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type Provider struct {
	url, model, key  string
	client           *http.Client
	mu               sync.Mutex
	window           time.Time
	remaining, limit int
}

// ConfiguredProviders reads optional, separate inference endpoints.
func ConfiguredProviders() (*Provider, *Provider, error) {
	embeddings, err := providerFromEnv("TOPIC_EMBEDDINGS", 60)
	if err != nil {
		return nil, nil, err
	}
	llm, err := providerFromEnv("TOPIC_LLM", 10)
	return embeddings, llm, err
}

func providerFromEnv(prefix string, limit int) (*Provider, error) {
	endpoint := strings.TrimSpace(os.Getenv(prefix + "_URL"))
	if endpoint == "" {
		return nil, nil
	}
	name := strings.TrimSpace(os.Getenv(prefix + "_MODEL"))
	target, err := url.Parse(endpoint)
	if err != nil || target.Hostname() == "" || target.User != nil || target.Fragment != "" ||
		(target.Scheme != "http" && target.Scheme != "https") || name == "" {
		return nil, errors.New("invalid " + prefix + "_URL or " + prefix + "_MODEL")
	}
	return &Provider{url: endpoint, model: name, key: os.Getenv(prefix + "_API_KEY"), limit: limit,
		client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}}, nil
}

func (p *Provider) request(ctx context.Context, input, output any) error {
	p.mu.Lock()
	now := time.Now()
	if now.Sub(p.window) >= time.Minute {
		p.window, p.remaining = now, p.limit
	}
	allowed := p.remaining > 0
	if allowed {
		p.remaining--
	}
	p.mu.Unlock()
	if !allowed {
		return errors.New("topic inference limit reached")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	response, err := p.client.Do(req)
	if err != nil {
		return errors.New("topic inference unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("topic inference rejected request")
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(output)
}

func (p *Provider) embed(ctx context.Context, inputs []string) ([][]float32, error) {
	var result struct {
		Data []struct {
			Index  int       `json:"index"`
			Vector []float32 `json:"embedding"`
		} `json:"data"`
	}
	err := p.request(ctx, map[string]any{"model": p.model, "input": inputs}, &result)
	if err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(inputs))
	for _, entry := range result.Data {
		if entry.Index < 0 || entry.Index >= len(vectors) || vectors[entry.Index] != nil || !validVector(entry.Vector) {
			return nil, errors.New("invalid embedding")
		}
		vectors[entry.Index] = entry.Vector
	}
	for _, vector := range vectors {
		if vector == nil {
			return nil, errors.New("missing embedding")
		}
	}
	return vectors, nil
}

func validVector(value []float32) bool {
	if len(value) < 1 || len(value) > 4096 {
		return false
	}
	sum := 0.0
	for _, v := range value {
		sum += float64(v) * float64(v)
	}
	return sum > 0 && !math.IsInf(sum, 0) && !math.IsNaN(sum)
}
