package todo_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// allStatuses перечисляет все статусы в порядке объявления —
// по нему строятся матрицы переходов.
var allStatuses = []todo.Status{
	todo.StatusPending,
	todo.StatusInProgress,
	todo.StatusCompleted,
	todo.StatusCancelled,
}

func TestStatusString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status todo.Status
		want   string
	}{
		{name: "ожидает выполнения", status: todo.StatusPending, want: "pending"},
		{name: "в работе", status: todo.StatusInProgress, want: "in_progress"},
		{name: "выполнена", status: todo.StatusCompleted, want: "completed"},
		{name: "отменена", status: todo.StatusCancelled, want: "cancelled"},
		{name: "значение вне перечисления", status: todo.Status(200), want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.status.String(); got != tt.want {
				t.Errorf("Status(%d).String() = %q, ожидалось %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestParseStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    todo.Status
		wantErr error
	}{
		{name: "pending", input: "pending", want: todo.StatusPending},
		{name: "in_progress", input: "in_progress", want: todo.StatusInProgress},
		{name: "completed", input: "completed", want: todo.StatusCompleted},
		{name: "cancelled", input: "cancelled", want: todo.StatusCancelled},
		{name: "регистр не важен", input: "In_Progress", want: todo.StatusInProgress},
		{name: "пробелы по краям обрезаются", input: "  completed  ", want: todo.StatusCompleted},
		{name: "пустая строка отвергается", input: "", wantErr: todo.ErrUnknownStatus},
		{name: "неизвестное имя отвергается", input: "done", wantErr: todo.ErrUnknownStatus},
		{name: "дефис вместо подчёркивания не принимается", input: "in-progress", wantErr: todo.ErrUnknownStatus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := todo.ParseStatus(tt.input)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParseStatus(%q) вернул ошибку %v, ожидалась %v", tt.input, err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseStatus(%q) вернул неожиданную ошибку: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseStatus(%q) = %v, ожидалось %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestStatusIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   todo.Status
		wantName string
		want     bool
	}{
		{name: "ожидает выполнения", status: todo.StatusPending, wantName: "pending", want: true},
		{name: "в работе", status: todo.StatusInProgress, wantName: "in_progress", want: true},
		{name: "выполнена", status: todo.StatusCompleted, wantName: "completed", want: true},
		{name: "отменена", status: todo.StatusCancelled, wantName: "cancelled", want: true},
		{name: "значение вне перечисления", status: todo.Status(200), wantName: "unknown", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("Status(%d).IsValid() = %v, ожидалось %v", tt.status, got, tt.want)
			}
			if got := tt.status.String(); got != tt.wantName {
				t.Errorf("Status(%d).String() = %q, ожидалось %q", tt.status, got, tt.wantName)
			}
		})
	}
}

func TestStatusIsTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   todo.Status
		wantName string
		want     bool
	}{
		{name: "ожидает выполнения — не терминальный", status: todo.StatusPending, wantName: "pending", want: false},
		{name: "в работе — не терминальный", status: todo.StatusInProgress, wantName: "in_progress", want: false},
		{name: "выполнена — терминальный", status: todo.StatusCompleted, wantName: "completed", want: true},
		{name: "отменена — терминальный", status: todo.StatusCancelled, wantName: "cancelled", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.status.IsTerminal(); got != tt.want {
				t.Errorf("Status(%s).IsTerminal() = %v, ожидалось %v", tt.name, got, tt.want)
			}
			if got := tt.status.String(); got != tt.wantName {
				t.Errorf("Status(%d).String() = %q, ожидалось %q", tt.status, got, tt.wantName)
			}
		})
	}
}

func TestStatusTransitionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		from        todo.Status
		wantName    string
		wantAllowed []todo.Status
	}{
		{
			name:     "из ожидания можно начать, выполнить или отменить",
			from:     todo.StatusPending,
			wantName: "pending",
			wantAllowed: []todo.Status{
				todo.StatusInProgress,
				todo.StatusCompleted,
				todo.StatusCancelled,
			},
		},
		{
			name:     "из работы можно выполнить или отменить",
			from:     todo.StatusInProgress,
			wantName: "in_progress",
			wantAllowed: []todo.Status{
				todo.StatusCompleted,
				todo.StatusCancelled,
			},
		},
		{
			name:        "из выполненной нельзя никуда",
			from:        todo.StatusCompleted,
			wantName:    "completed",
			wantAllowed: nil,
		},
		{
			name:        "из отменённой нельзя никуда",
			from:        todo.StatusCancelled,
			wantName:    "cancelled",
			wantAllowed: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotAllowed []todo.Status
			for _, target := range allStatuses {
				if tt.from.CanTransitionTo(target) {
					gotAllowed = append(gotAllowed, target)
				}
			}

			if !slices.Equal(gotAllowed, tt.wantAllowed) {
				t.Errorf("из %s разрешены переходы в %v, ожидалось %v",
					tt.wantName, statusNames(gotAllowed), statusNames(tt.wantAllowed))
			}
			// Переход в самого себя переходом не считается: повторный Complete
			// должен упираться в ErrTaskAlreadyCompleted, а не проходить молча.
			if tt.from.CanTransitionTo(tt.from) {
				t.Errorf("переход %s → %s в самого себя должен быть запрещён", tt.wantName, tt.wantName)
			}
			if got := tt.from.String(); got != tt.wantName {
				t.Errorf("Status(%d).String() = %q, ожидалось %q", tt.from, got, tt.wantName)
			}
		})
	}
}

func TestStatusZeroValueIsPending(t *testing.T) {
	t.Parallel()

	var status todo.Status

	if status != todo.StatusPending {
		t.Errorf("нулевое значение Status = %v, ожидалось StatusPending", status)
	}
	if !status.IsValid() {
		t.Error("нулевое значение Status невалидно, ожидалось валидное")
	}
	if got := status.String(); got != "pending" {
		t.Errorf("нулевое значение Status.String() = %q, ожидалось %q", got, "pending")
	}
}

// statusNames переводит список статусов в имена — иначе в диагностике
// видны голые числа.
func statusNames(statuses []todo.Status) []string {
	names := make([]string, len(statuses))
	for i, s := range statuses {
		names[i] = s.String()
	}
	return names
}
