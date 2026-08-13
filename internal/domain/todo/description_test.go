package todo_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/deliseev/todoer/internal/domain/todo"
)

func TestNewDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      string
		wantEmpty bool
		wantErr   error
	}{
		{
			name:      "пустое описание допустимо",
			input:     "",
			want:      "",
			wantEmpty: true,
		},
		{
			name:      "описание из одних пробелов схлопывается в пустое",
			input:     "   \t  ",
			want:      "",
			wantEmpty: true,
		},
		{
			name:  "пробелы по краям обрезаются",
			input: "  Два литра, в магазине у дома  ",
			want:  "Два литра, в магазине у дома",
		},
		{
			name:  "переносы строк внутри сохраняются",
			input: "Первая строка\nВторая строка",
			want:  "Первая строка\nВторая строка",
		},
		{
			name:  "описание ровно предельной длины принимается",
			input: strings.Repeat("a", todo.MaxDescriptionLength),
			want:  strings.Repeat("a", todo.MaxDescriptionLength),
		},
		{
			name:    "описание на одну руну длиннее предела отвергается",
			input:   strings.Repeat("a", todo.MaxDescriptionLength+1),
			wantErr: todo.ErrDescriptionTooLong,
		},
		{
			name:  "предел считается в рунах, а не в байтах",
			input: strings.Repeat("я", todo.MaxDescriptionLength),
			want:  strings.Repeat("я", todo.MaxDescriptionLength),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := todo.NewDescription(tt.input)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewDescription(...) вернул ошибку %v, ожидалась %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("NewDescription(...) вернул неожиданную ошибку: %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("NewDescription(...).String() = %q, ожидалось %q", got.String(), tt.want)
			}
			if got.IsEmpty() != tt.wantEmpty {
				t.Errorf("NewDescription(...).IsEmpty() = %v, ожидалось %v", got.IsEmpty(), tt.wantEmpty)
			}
		})
	}
}

func TestDescriptionZeroValue(t *testing.T) {
	t.Parallel()

	var description todo.Description

	if !description.IsEmpty() {
		t.Error("Description{}.IsEmpty() = false, ожидалось true")
	}
	if description.String() != "" {
		t.Errorf("Description{}.String() = %q, ожидалась пустая строка", description.String())
	}
}
