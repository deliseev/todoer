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

func TestPriorityRank(t *testing.T) {
	t.Parallel()

	// Ранг — порядок важности, и взять его больше неоткуда. Имя не годится:
	// по алфавиту critical идёт раньше high. Значение константы не годится
	// тоже: нулём обязан быть PriorityNormal — нулевое значение осмысленно, —
	// и из-за этого PriorityLow больше обычного по числу, будучи меньше по
	// важности.
	t.Run("ранги возрастают вместе с важностью", func(t *testing.T) {
		t.Parallel()

		ascending := []todo.Priority{
			todo.PriorityLow,
			todo.PriorityNormal,
			todo.PriorityHigh,
			todo.PriorityCritical,
		}

		// Проверяются отношения, а не конкретные числа: правило — порядок,
		// а числа деталь. Привязка к ним запретила бы вставить новый приоритет
		// между существующими, ничего при этом не сломав.
		for i := 1; i < len(ascending); i++ {
			lower, higher := ascending[i-1], ascending[i]

			if lower.Rank() >= higher.Rank() {
				t.Errorf("ранг %v = %d, ранг %v = %d, ожидалось строгое возрастание",
					lower, lower.Rank(), higher, higher.Rank())
			}
		}
	})

	t.Run("порядок важности не совпадает с порядком констант", func(t *testing.T) {
		t.Parallel()

		// Ради этого таблица рангов и заводится: свести Rank к int(p) выглядит
		// соблазнительно, компилируется и молча ломает сортировку.
		if todo.PriorityNormal >= todo.PriorityLow {
			t.Fatal("PriorityNormal больше не меньше PriorityLow по значению — проверка потеряла смысл")
		}
		if todo.PriorityNormal.Rank() <= todo.PriorityLow.Rank() {
			t.Errorf("ранг обычного = %d, ранг низкого = %d, ожидалось, что обычный важнее",
				todo.PriorityNormal.Rank(), todo.PriorityLow.Rank())
		}
	})

	t.Run("ранги допустимых приоритетов различны", func(t *testing.T) {
		t.Parallel()

		// Общий ранг на двоих склеил бы два приоритета в сортировке, и порядок
		// внутри пары стал бы делом случая.
		seen := make(map[int]todo.Priority)

		for value := range 256 {
			priority := todo.Priority(value)
			if !priority.IsValid() {
				continue
			}

			if other, taken := seen[priority.Rank()]; taken {
				t.Errorf("%v и %v делят ранг %d", other, priority, priority.Rank())
			}
			seen[priority.Rank()] = priority
		}
	})

	t.Run("ранг есть ровно у допустимых приоритетов", func(t *testing.T) {
		t.Parallel()

		// Таблица рангов и таблица имён обязаны идти в ногу: новый приоритет,
		// забытый в рангах, иначе молча получил бы чужой ранг и сел не на своё
		// место. Невозможный ранг у недопустимого значения — то же решение,
		// что "unknown" у String: тихий ноль слился бы с законным рангом.
		const unknownRank = -1

		for value := range 256 {
			priority := todo.Priority(value)

			switch {
			case priority.IsValid() && priority.Rank() == unknownRank:
				t.Errorf("Priority(%d).Rank() = %d, а приоритет допустим", value, unknownRank)
			case !priority.IsValid() && priority.Rank() != unknownRank:
				t.Errorf("Priority(%d).Rank() = %d, ожидалось %d: приоритет недопустим",
					value, priority.Rank(), unknownRank)
			}
		}
	})
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
