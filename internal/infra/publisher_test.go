package infra_test

import (
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
	"github.com/deliseev/todoer/internal/infra"
)

// Опорный момент и владелец задачи, на которой проверяется заглушка.
// Публикатор о них ничего не знает — ему нужна просто какая-нибудь партия
// настоящих событий.
var testNow = time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)

const testOwner = "user-42"

// Заглушка обязана удовлетворять порту app.EventPublisher.
var _ app.EventPublisher = infra.NopPublisher{}

func TestNopPublisher(t *testing.T) {
	t.Parallel()

	t.Run("партия событий принимается молча", func(t *testing.T) {
		t.Parallel()

		task := todotest.NewTask(t, testOwner, testNow)

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
