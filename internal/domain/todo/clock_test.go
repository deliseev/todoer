package todo_test

import (
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// Системные часы обязаны удовлетворять порту Clock.
var _ todo.Clock = todo.SystemClock{}

func TestSystemClockNow(t *testing.T) {
	t.Parallel()

	before := time.Now()
	got := todo.SystemClock{}.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("SystemClock.Now() = %s, ожидалось значение между %s и %s", got, before, after)
	}
	if loc := got.Location(); loc != time.UTC {
		t.Errorf("SystemClock.Now().Location() = %s, ожидалось UTC", loc)
	}
}
