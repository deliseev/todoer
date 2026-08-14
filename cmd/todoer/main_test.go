package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewTaskService(t *testing.T) {
	t.Parallel()

	t.Run("боевые реализации подходят портам", func(t *testing.T) {
		t.Parallel()

		service, err := newTaskService()
		if err != nil {
			t.Fatalf("newTaskService() вернул ошибку: %v", err)
		}
		if service == nil {
			t.Fatal("newTaskService() вернул nil без ошибки")
		}
	})
}

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("задача проходит жизненный цикл целиком", func(t *testing.T) {
		t.Parallel()

		var out strings.Builder

		if err := run(t.Context(), &out); err != nil {
			t.Fatalf("run(...) вернул ошибку: %v", err)
		}

		for _, want := range []string{"задача создана", "задача в работе", "задача выполнена"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("в выводе нет %q, получено:\n%s", want, out.String())
			}
		}
	})

	t.Run("отменённый запрос останавливает работу", func(t *testing.T) {
		t.Parallel()

		var out strings.Builder

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		// Отмена доходит до сценария через хранилище: сборка настоящая,
		// поэтому проверяется весь путь, а не поведение подставного порта.
		if err := run(ctx, &out); !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась context.Canceled, получено: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("отменённый запуск что-то напечатал:\n%s", out.String())
		}
	})
}
