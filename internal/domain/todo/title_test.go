package todo_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

func TestNewTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{
			name:    "пустая строка отвергается",
			input:   "",
			wantErr: todo.ErrEmptyTitle,
		},
		{
			name:    "строка из одних пробельных символов отвергается",
			input:   " \t\n\r ",
			wantErr: todo.ErrEmptyTitle,
		},
		{
			name:  "пробелы по краям обрезаются",
			input: "   Купить молоко\t",
			want:  "Купить молоко",
		},
		{
			name:  "внутренние пробелы сохраняются как есть",
			input: "Купить  молоко  и  хлеб",
			want:  "Купить  молоко  и  хлеб",
		},
		{
			name:  "заголовок ровно предельной длины принимается",
			input: strings.Repeat("a", todo.MaxTitleLength),
			want:  strings.Repeat("a", todo.MaxTitleLength),
		},
		{
			name:    "заголовок на одну руну длиннее предела отвергается",
			input:   strings.Repeat("a", todo.MaxTitleLength+1),
			wantErr: todo.ErrTitleTooLong,
		},
		{
			// Предел считается в рунах: 256 кириллических символов — это
			// 512 байт, и наивный len() ошибочно отверг бы такой заголовок.
			name:  "предел считается в рунах, а не в байтах",
			input: strings.Repeat("я", todo.MaxTitleLength),
			want:  strings.Repeat("я", todo.MaxTitleLength),
		},
		{
			name:    "кириллица на одну руну длиннее предела отвергается",
			input:   strings.Repeat("я", todo.MaxTitleLength+1),
			wantErr: todo.ErrTitleTooLong,
		},
		{
			name:  "многобайтные эмодзи считаются по одной руне",
			input: strings.Repeat("🙂", todo.MaxTitleLength),
			want:  strings.Repeat("🙂", todo.MaxTitleLength),
		},
		{
			// Сначала обрезаем, потом меряем: иначе пробелы съедали бы лимит.
			name:  "длина проверяется после обрезки пробелов",
			input: "  " + strings.Repeat("a", todo.MaxTitleLength) + "  ",
			want:  strings.Repeat("a", todo.MaxTitleLength),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := todo.NewTitle(tt.input)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewTitle(...) вернул ошибку %v, ожидалась %v", err, tt.wantErr)
				}
				if !got.IsZero() {
					t.Errorf("NewTitle(...) при ошибке вернул непустой заголовок %q", got.String())
				}
				return
			}

			if err != nil {
				t.Fatalf("NewTitle(...) вернул неожиданную ошибку: %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("NewTitle(...).String() = %q, ожидалось %q", got.String(), tt.want)
			}
			if got.IsZero() {
				t.Error("NewTitle(...).IsZero() = true, ожидалось false для созданного заголовка")
			}
		})
	}
}

func TestTitleZeroValue(t *testing.T) {
	t.Parallel()

	var title todo.Title

	if !title.IsZero() {
		t.Error("Title{}.IsZero() = false, ожидалось true")
	}
	if title.String() != "" {
		t.Errorf("Title{}.String() = %q, ожидалась пустая строка", title.String())
	}
}

func TestTitleIsComparable(t *testing.T) {
	t.Parallel()

	// Значимые объекты сравниваются по значению: одинаковый смысл —
	// одинаковое значение, даже если исходные строки отличались пробелами.
	first := todotest.MustTitle(t, "Купить молоко")
	second := todotest.MustTitle(t, "  Купить молоко  ")
	other := todotest.MustTitle(t, "Купить хлеб")

	if first != second {
		t.Errorf("заголовки %q и %q должны быть равны", first.String(), second.String())
	}
	if first == other {
		t.Errorf("заголовки %q и %q не должны быть равны", first.String(), other.String())
	}
}
