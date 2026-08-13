package todo_test

import (
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// testNow — фиксированная точка отсчёта для всех тестов домена.
// Домен не ходит за временем сам, поэтому «сейчас» здесь — обычная константа,
// и тесты не зависят от реальных часов.
var testNow = time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)

// mustTitle создаёт заголовок или валит тест.
func mustTitle(t *testing.T, s string) todo.Title {
	t.Helper()

	title, err := todo.NewTitle(s)
	if err != nil {
		t.Fatalf("NewTitle(%q) вернул ошибку: %v", s, err)
	}
	return title
}

// mustDescription создаёт описание или валит тест.
func mustDescription(t *testing.T, s string) todo.Description {
	t.Helper()

	description, err := todo.NewDescription(s)
	if err != nil {
		t.Fatalf("NewDescription(%q) вернул ошибку: %v", s, err)
	}
	return description
}

// mustTaskID создаёт новый идентификатор задачи или валит тест.
func mustTaskID(t *testing.T) todo.TaskID {
	t.Helper()

	id, err := todo.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() вернул ошибку: %v", err)
	}
	return id
}

// mustOwnerID разбирает идентификатор владельца или валит тест.
func mustOwnerID(t *testing.T, s string) todo.OwnerID {
	t.Helper()

	id, err := todo.ParseOwnerID(s)
	if err != nil {
		t.Fatalf("ParseOwnerID(%q) вернул ошибку: %v", s, err)
	}
	return id
}

// mustDueDate создаёт срок выполнения относительно testNow или валит тест.
func mustDueDate(t *testing.T, at time.Time) *todo.DueDate {
	t.Helper()

	due, err := todo.NewDueDate(at, testNow)
	if err != nil {
		t.Fatalf("NewDueDate(%s, %s) вернул ошибку: %v", at, testNow, err)
	}
	return &due
}

// newTestTask создаёт типовую задачу: обычный приоритет, без срока,
// создана в момент testNow.
func newTestTask(t *testing.T) *todo.Task {
	t.Helper()

	return newTestTaskWithDueDate(t, nil)
}

// newTestTaskWithDueDate создаёт типовую задачу с заданным сроком.
func newTestTaskWithDueDate(t *testing.T, due *todo.DueDate) *todo.Task {
	t.Helper()

	task, err := todo.NewTask(
		mustTaskID(t),
		mustOwnerID(t, "user-42"),
		mustTitle(t, "Купить молоко"),
		mustDescription(t, "Два литра, в магазине у дома"),
		todo.PriorityNormal,
		due,
		testNow,
	)
	if err != nil {
		t.Fatalf("NewTask(...) вернул ошибку: %v", err)
	}
	return task
}

// eventNames раскладывает события в список имён — так порядок и состав
// читаются в диагностике одной строкой.
func eventNames(events []todo.DomainEvent) []string {
	names := make([]string, len(events))
	for i, e := range events {
		names[i] = e.EventName()
	}
	return names
}
