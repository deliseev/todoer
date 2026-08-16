package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// TestRunRejectsBadInvocation: разбор аргументов не зависит от базы и обязан
// отказывать до похода в неё.
func TestRunRejectsBadInvocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		dsn  string
	}{
		{name: "без команды", args: nil, dsn: "postgres://localhost/todoer"},
		{name: "две команды", args: []string{"up", "down"}, dsn: "postgres://localhost/todoer"},
		{name: "неизвестная команда", args: []string{"upp"}, dsn: "postgres://localhost/todoer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := run(t.Context(), io.Discard, tt.args, tt.dsn)

			if !errors.Is(err, errUsage) {
				t.Errorf("run(...) вернул ошибку %v, ожидалась errUsage", err)
			}
		})
	}
}

// TestRunRequiresDSN: без строки подключения команда обязана внятно отказать,
// а не пытаться подключиться к базе по умолчанию.
func TestRunRequiresDSN(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"up", "down", "status"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			err := run(t.Context(), io.Discard, []string{command}, "")

			if err == nil {
				t.Fatal("run(...) без строки подключения вернул nil")
			}
			if !strings.Contains(err.Error(), dsnEnv) {
				t.Errorf("ошибка = %q, ожидалось упоминание %s", err, dsnEnv)
			}
		})
	}
}
