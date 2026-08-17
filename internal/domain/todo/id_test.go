package todo_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

func TestNewTaskID(t *testing.T) {
	t.Parallel()

	id, err := todo.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() вернул ошибку: %v", err)
	}

	if id.IsZero() {
		t.Error("NewTaskID().IsZero() = true, ожидалось false")
	}
	if got := len(id.String()); got != todo.TaskIDLength {
		t.Errorf("длина NewTaskID().String() = %d, ожидалось %d", got, todo.TaskIDLength)
	}
	if got := id.String(); strings.ToLower(got) != got {
		t.Errorf("NewTaskID().String() = %q, ожидался нижний регистр", got)
	}
}

func TestNewTaskIDIsUnique(t *testing.T) {
	t.Parallel()

	const attempts = 1000

	seen := make(map[todo.TaskID]struct{}, attempts)
	for range attempts {
		id, err := todo.NewTaskID()
		if err != nil {
			t.Fatalf("NewTaskID() вернул ошибку: %v", err)
		}
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("NewTaskID() вернул повторяющийся идентификатор %q", id.String())
		}
		seen[id] = struct{}{}
	}
}

func TestNewTaskIDIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	// Идентификаторы раздаются на каждый входящий запрос, то есть из многих
	// горутин сразу: генератор обязан быть потокобезопасным сам по себе,
	// без блокировки на стороне вызывающего.
	const (
		goroutines   = 8
		perGoroutine = 256
		total        = goroutines * perGoroutine
	)

	ids := make(chan todo.TaskID, total)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range perGoroutine {
				id, err := todo.NewTaskID()
				if err != nil {
					t.Errorf("NewTaskID() вернул ошибку: %v", err)
					return
				}
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[todo.TaskID]struct{}, total)
	for id := range ids {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("NewTaskID() вернул повторяющийся идентификатор %q", id.String())
		}
		seen[id] = struct{}{}
	}

	if len(seen) != total {
		t.Errorf("получено %d идентификаторов, ожидалось %d", len(seen), total)
	}
}

func TestParseTaskID(t *testing.T) {
	t.Parallel()

	const valid = "0123456789abcdef0123456789abcdef"

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{name: "корректный идентификатор", input: valid, want: valid},
		{name: "пробелы по краям обрезаются", input: "  " + valid + "  ", want: valid},
		{name: "верхний регистр приводится к нижнему", input: strings.ToUpper(valid), want: valid},
		{name: "пустая строка отвергается", input: "", wantErr: todo.ErrInvalidTaskID},
		{name: "строка из пробелов отвергается", input: "   ", wantErr: todo.ErrInvalidTaskID},
		{name: "слишком короткий идентификатор отвергается", input: valid[:31], wantErr: todo.ErrInvalidTaskID},
		{name: "слишком длинный идентификатор отвергается", input: valid + "0", wantErr: todo.ErrInvalidTaskID},
		{name: "не-hex символы отвергаются", input: strings.Repeat("z", todo.TaskIDLength), wantErr: todo.ErrInvalidTaskID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := todo.ParseTaskID(tt.input)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParseTaskID(%q) вернул ошибку %v, ожидалась %v", tt.input, err, tt.wantErr)
				}
				if !got.IsZero() {
					t.Errorf("ParseTaskID(%q) при ошибке вернул непустой идентификатор", tt.input)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseTaskID(%q) вернул неожиданную ошибку: %v", tt.input, err)
			}
			if got.String() != tt.want {
				t.Errorf("ParseTaskID(%q).String() = %q, ожидалось %q", tt.input, got.String(), tt.want)
			}
		})
	}
}

func TestTaskIDRoundTrip(t *testing.T) {
	t.Parallel()

	original := todotest.MustTaskID(t)

	restored, err := todo.ParseTaskID(original.String())
	if err != nil {
		t.Fatalf("ParseTaskID(%q) вернул ошибку: %v", original.String(), err)
	}
	if restored != original {
		t.Errorf("после обхода String→Parse получили %q, ожидалось %q", restored.String(), original.String())
	}
}

func TestTaskIDZeroValue(t *testing.T) {
	t.Parallel()

	var id todo.TaskID

	if !id.IsZero() {
		t.Error("TaskID{}.IsZero() = false, ожидалось true")
	}
	if id.String() != "" {
		t.Errorf("TaskID{}.String() = %q, ожидалась пустая строка", id.String())
	}
}

func TestParseOwnerID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{name: "непрозрачный идентификатор принимается как есть", input: "user-42", want: "user-42"},
		{name: "идентификатор из чужого контекста в любом формате", input: "auth0|6512ab", want: "auth0|6512ab"},
		{name: "пробелы по краям обрезаются", input: "  user-42  ", want: "user-42"},
		{name: "пустая строка отвергается", input: "", wantErr: todo.ErrInvalidOwnerID},
		{name: "строка из пробелов отвергается", input: " \t ", wantErr: todo.ErrInvalidOwnerID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := todo.ParseOwnerID(tt.input)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParseOwnerID(%q) вернул ошибку %v, ожидалась %v", tt.input, err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseOwnerID(%q) вернул неожиданную ошибку: %v", tt.input, err)
			}
			if got.String() != tt.want {
				t.Errorf("ParseOwnerID(%q).String() = %q, ожидалось %q", tt.input, got.String(), tt.want)
			}
			if got.IsZero() {
				t.Error("ParseOwnerID(...).IsZero() = true, ожидалось false")
			}
		})
	}
}

func TestOwnerIDZeroValue(t *testing.T) {
	t.Parallel()

	var id todo.OwnerID

	if !id.IsZero() {
		t.Error("OwnerID{}.IsZero() = false, ожидалось true")
	}
	if id.String() != "" {
		t.Errorf("OwnerID{}.String() = %q, ожидалась пустая строка", id.String())
	}
}
