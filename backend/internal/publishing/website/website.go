package website

import (
	"context"
	"strconv"

	"smmagent/internal/model"
)

type Publisher struct{}

// Send returns the stable public ID. The post becomes visible when its state
// is committed as sent in PostgreSQL.
func (Publisher) Send(_ context.Context, post model.Publication, _ string) (string, error) {
	return strconv.FormatInt(post.ID, 10), nil
}
