package todo_test

import (
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

// testNow — фиксированная точка отсчёта для всех тестов домена.
// Домен не ходит за временем сам, поэтому «сейчас» здесь — обычная константа,
// и тесты не зависят от реальных часов.
var testNow = time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)

// newTestTask создаёт типовую задачу: обычный приоритет, без срока,
// создана в момент testNow.
//
// Своя, а не todotest.NewTask: тестам домена нужна задача именно без срока,
// и рядом с ней — вариант с любым заданным сроком.
func newTestTask(t *testing.T) *todo.Task {
	t.Helper()

	return newTestTaskWithDueDate(t, nil)
}

// newTestTaskWithDueDate создаёт типовую задачу с заданным сроком.
func newTestTaskWithDueDate(t *testing.T, due *todo.DueDate) *todo.Task {
	t.Helper()

	task, err := todo.NewTask(
		todotest.MustTaskID(t),
		todotest.MustOwnerID(t, "user-42"),
		todotest.MustTitle(t, "Купить молоко"),
		todotest.MustDescription(t, "Два литра, в магазине у дома"),
		todo.PriorityNormal,
		due,
		testNow,
	)
	if err != nil {
		t.Fatalf("NewTask(...) вернул ошибку: %v", err)
	}
	return task
}
