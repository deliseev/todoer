package infra_test

import (
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// Опорные моменты времени. Хранилище о времени не думает — оно принимает
// то, что уже проставил домен, — поэтому здесь достаточно констант.
var (
	testNow   = time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	testLater = testNow.Add(time.Hour)
	testDue   = testNow.Add(24 * time.Hour)
)

// testOwner — владелец задач в тестах хранилища.
const testOwner = "user-42"

// newTestTask создаёт типовую задачу со сроком: обычный приоритет,
// первая версия, статус pending.
func newTestTask(t *testing.T) *todo.Task {
	t.Helper()

	task, err := todo.NewTask(
		mustTaskID(t),
		mustOwnerID(t, testOwner),
		mustTitle(t, "Купить молоко"),
		mustDescription(t, "Два литра, в магазине у дома"),
		todo.PriorityNormal,
		mustDueDate(t, testDue),
		testNow,
	)
	if err != nil {
		t.Fatalf("NewTask(...) вернул ошибку: %v", err)
	}
	return task
}

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

// mustRename переименовывает задачу или валит тест — типовая мутация,
// которой тесты хранилища двигают версию.
func mustRename(t *testing.T, task *todo.Task, title string, now time.Time) {
	t.Helper()

	if err := task.Rename(mustTitle(t, title), now); err != nil {
		t.Fatalf("Rename(%q) вернул ошибку: %v", title, err)
	}
}
