package todo_test

import (
	"errors"
	"testing"

	"github.com/deliseev/todoer/internal/domain/todo"
)

func TestPriorityString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		priority todo.Priority
		want     string
	}{
		{name: "низкий", priority: todo.PriorityLow, want: "low"},
		{name: "обычный", priority: todo.PriorityNormal, want: "normal"},
		{name: "высокий", priority: todo.PriorityHigh, want: "high"},
		{name: "критический", priority: todo.PriorityCritical, want: "critical"},
		{name: "значение вне перечисления", priority: todo.Priority(200), want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.priority.String(); got != tt.want {
				t.Errorf("Priority(%d).String() = %q, ожидалось %q", tt.priority, got, tt.want)
			}
		})
	}
}

func TestPriorityIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		priority todo.Priority
		wantName string
		want     bool
	}{
		{name: "низкий", priority: todo.PriorityLow, wantName: "low", want: true},
		{name: "обычный", priority: todo.PriorityNormal, wantName: "normal", want: true},
		{name: "высокий", priority: todo.PriorityHigh, wantName: "high", want: true},
		{name: "критический", priority: todo.PriorityCritical, wantName: "critical", want: true},
		{name: "значение вне перечисления", priority: todo.Priority(200), wantName: "unknown", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.priority.IsValid(); got != tt.want {
				t.Errorf("Priority(%d).IsValid() = %v, ожидалось %v", tt.priority, got, tt.want)
			}
			if got := tt.priority.String(); got != tt.wantName {
				t.Errorf("Priority(%d).String() = %q, ожидалось %q", tt.priority, got, tt.wantName)
			}
		})
	}
}

func TestParsePriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    todo.Priority
		wantErr error
	}{
		{name: "low", input: "low", want: todo.PriorityLow},
		{name: "normal", input: "normal", want: todo.PriorityNormal},
		{name: "high", input: "high", want: todo.PriorityHigh},
		{name: "critical", input: "critical", want: todo.PriorityCritical},
		{name: "регистр не важен", input: "HiGh", want: todo.PriorityHigh},
		{name: "пробелы по краям обрезаются", input: "  critical  ", want: todo.PriorityCritical},
		{name: "пустая строка отвергается", input: "", wantErr: todo.ErrUnknownPriority},
		{name: "неизвестное имя отвергается", input: "urgent", wantErr: todo.ErrUnknownPriority},
		{name: "числовое представление не принимается", input: "2", wantErr: todo.ErrUnknownPriority},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := todo.ParsePriority(tt.input)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParsePriority(%q) вернул ошибку %v, ожидалась %v", tt.input, err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParsePriority(%q) вернул неожиданную ошибку: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParsePriority(%q) = %v, ожидалось %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestPriorityParseStringRoundTrip(t *testing.T) {
	t.Parallel()

	priorities := map[string]todo.Priority{
		"низкий":      todo.PriorityLow,
		"обычный":     todo.PriorityNormal,
		"высокий":     todo.PriorityHigh,
		"критический": todo.PriorityCritical,
	}

	for name, want := range priorities {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := todo.ParsePriority(want.String())
			if err != nil {
				t.Fatalf("ParsePriority(%q) вернул ошибку: %v", want.String(), err)
			}
			if got != want {
				t.Errorf("после обхода String→Parse получили %v, ожидалось %v", got, want)
			}
		})
	}
}

func TestPriorityZeroValueIsNormal(t *testing.T) {
	t.Parallel()

	// Нулевое значение должно быть осмысленным: задача, созданная без явного
	// приоритета, обычная, а не «неизвестная».
	var priority todo.Priority

	if priority != todo.PriorityNormal {
		t.Errorf("нулевое значение Priority = %v, ожидалось PriorityNormal", priority)
	}
	if !priority.IsValid() {
		t.Error("нулевое значение Priority невалидно, ожидалось валидное")
	}
	if got := priority.String(); got != "normal" {
		t.Errorf("нулевое значение Priority.String() = %q, ожидалось %q", got, "normal")
	}
}
