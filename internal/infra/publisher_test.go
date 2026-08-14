package infra_test

import (
	"testing"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/infra"
)

// Заглушка обязана удовлетворять порту app.EventPublisher.
var _ app.EventPublisher = infra.NopPublisher{}

func TestNopPublisher(t *testing.T) {
	t.Parallel()

	t.Run("партия событий принимается молча", func(t *testing.T) {
		t.Parallel()

		task := newTestTask(t)

		if err := (infra.NopPublisher{}).Publish(t.Context(), task.PullEvents()); err != nil {
			t.Fatalf("Publish(...) вернул ошибку: %v", err)
		}
	})

	t.Run("пустая партия ошибкой не считается", func(t *testing.T) {
		t.Parallel()

		if err := (infra.NopPublisher{}).Publish(t.Context(), nil); err != nil {
			t.Fatalf("Publish(...) вернул ошибку: %v", err)
		}
	})
}
