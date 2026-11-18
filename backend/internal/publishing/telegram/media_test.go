package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"smmagent/internal/model"
)

type memoryMedia struct{}

func (memoryMedia) Open(context.Context, model.Media) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("fixture-from-s3")), nil
}

func TestMediaUploadAndAlbum(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			p := testPublisher(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("chat_id") != "@channel" {
					t.Fatal("missing destination")
				}
				field := "photo"
				if count == 2 {
					field = "file0"
				}
				file, _, err := r.FormFile(field)
				if err != nil {
					t.Fatal(err)
				}
				data, _ := io.ReadAll(file)
				file.Close()
				if string(data) != "fixture-from-s3" {
					t.Fatal("wrong attachment")
				}
				if count == 1 {
					if r.URL.Path != "/sendPhoto" || r.FormValue("parse_mode") != "HTML" {
						t.Fatal("wrong media request")
					}
					fmt.Fprint(w, `{"ok":true,"result":{"message_id":51}}`)
				} else {
					if r.URL.Path != "/sendMediaGroup" || !strings.Contains(r.FormValue("media"), "attach://file1") {
						t.Fatal("wrong album")
					}
					fmt.Fprint(w, `{"ok":true,"result":[{"message_id":51},{"message_id":52}]}`)
				}
			})
			p.Media = memoryMedia{}
			p.TempDir = t.TempDir()
			post := model.Publication{Destination: "@channel", Title: "Новость", Body: "Текст"}
			for range count {
				post.Media = append(post.Media, model.Media{Kind: "photo", URL: "https://example.org/a.jpg"})
			}
			id, err := p.Send(t.Context(), post, "")
			if err != nil || id != "51" {
				t.Fatalf("id=%s error=%v", id, err)
			}
			files, _ := os.ReadDir(p.TempDir)
			if len(files) != 0 {
				t.Fatal("staging files leaked")
			}
		})
	}
}

func TestFormattedCaptionEscapesAndFits(t *testing.T) {
	post := model.Publication{Title: "<новость>", Body: strings.Repeat("&🙂", 2000),
		ChannelName: "Канал", Destination: "@channel", SourceURL: "https://example.org/story"}
	text := renderLimit(post, false, 1024)
	if !strings.HasPrefix(text, "<b>&lt;новость&gt;</b>") || !strings.Contains(text, "<i>") ||
		!strings.HasSuffix(text, "\n\n👉 <a href=\"https://t.me/channel\">Канал. Подписаться</a>") || utf16Len(text) > 1024 {
		t.Fatalf("invalid caption: %s", text)
	}
}
