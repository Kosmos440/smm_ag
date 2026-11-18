package media

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"testing"

	"smmagent/internal/model"
)

type sourceTransport func(*http.Request) (*http.Response, error)

func (f sourceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestS3MediaRoundTrip(t *testing.T) {
	endpoint := os.Getenv("SMM_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("SMM_TEST_S3_ENDPOINT is not set")
	}
	store := New(endpoint, os.Getenv("S3_BUCKET"), os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"), t.TempDir())
	response, err := http.Get(endpoint + "/" + os.Getenv("S3_BUCKET"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous S3 status = %d", response.StatusCode)
	}
	// Sniffable JPEG bytes. No source network access is used in this fixture.
	original := append([]byte{0xff, 0xd8, 0xff}, bytes.Repeat([]byte{1}, 600)...)
	downloads := 0
	store.public = &http.Client{Transport: sourceTransport(func(r *http.Request) (*http.Response, error) {
		downloads++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(original)), Header: make(http.Header)}, nil
	})}
	item := model.Media{URL: "https://example.org/s3-fixture-" + t.Name() + ".jpg", Kind: "photo"}
	for range 2 {
		body, err := store.Open(t.Context(), item)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(body)
		body.Close()
		if err != nil || !bytes.Equal(got, original) {
			t.Fatalf("S3 bytes differ: %v", err)
		}
	}
	if downloads > 1 {
		t.Fatalf("downloaded original %d times", downloads)
	}
}
