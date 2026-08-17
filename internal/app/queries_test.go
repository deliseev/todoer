package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

func TestGetTask(t *testing.T) {
	t.Run("задача читается целиком", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusInProgress)

		view, err := env.service.GetTask(t.Context(), app.GetTaskQuery{
			TaskID: task.ID().String(), OwnerID: testOwner,
		})
		if err != nil {
			t.Fatalf("GetTask(...) вернул ошибку: %v", err)
		}

		if view.ID != task.ID().String() {
			t.Errorf("id = %q, ожидался %q", view.ID, task.ID())
		}
		if view.OwnerID != testOwner {
			t.Errorf("владелец = %q, ожидался %q", view.OwnerID, testOwner)
		}
		if view.Title != task.Title().String() {
			t.Errorf("заголовок = %q, ожидался %q", view.Title, task.Title())
		}
		if view.Description != task.Description().String() {
			t.Errorf("описание = %q, ожидалось %q", view.Description, task.Description())
		}
		if view.Status != todo.StatusInProgress.String() {
			t.Errorf("статус = %q, ожидался %q", view.Status, todo.StatusInProgress)
		}
		if view.Priority != task.Priority().String() {
			t.Errorf("приоритет = %q, ожидался %q", view.Priority, task.Priority())
		}
		if view.Version != task.Version() {
			t.Errorf("версия = %d, ожидалась %d", view.Version, task.Version())
		}
		if view.DueDate == nil {
			t.Fatal("срок потерян")
		}
		if !view.DueDate.Equal(testDue) {
			t.Errorf("срок = %s, ожидался %s", view.DueDate, testDue)
		}
		if !view.CreatedAt.Equal(testNow) {
			t.Errorf("createdAt = %s, ожидалось %s", view.CreatedAt, testNow)
		}
	})

	t.Run("выполненная задача несёт момент завершения", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusCompleted)

		view, err := env.service.GetTask(t.Context(), app.GetTaskQuery{
			TaskID: task.ID().String(), OwnerID: testOwner,
		})
		if err != nil {
			t.Fatalf("GetTask(...) вернул ошибку: %v", err)
		}
		if view.CompletedAt == nil {
			t.Fatal("момент завершения потерян")
		}
		if !view.CompletedAt.Equal(testNow) {
			t.Errorf("completedAt = %s, ожидалось %s", view.CompletedAt, testNow)
		}
	})

	t.Run("чтение не порождает ни записи, ни событий", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		if _, err := env.service.GetTask(t.Context(), app.GetTaskQuery{
			TaskID: task.ID().String(), OwnerID: testOwner,
		}); err != nil {
			t.Fatalf("GetTask(...) вернул ошибку: %v", err)
		}

		if env.repo.saveCount() != 0 {
			t.Error("чтение дошло до записи")
		}
		if env.publisher.callCount() != 0 {
			t.Error("чтение породило публикацию")
		}
	})

	t.Run("чужая задача неотличима от несуществующей", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		_, err := env.service.GetTask(t.Context(), app.GetTaskQuery{
			TaskID: task.ID().String(), OwnerID: otherOwner,
		})
		if !errors.Is(err, app.ErrTaskNotFound) {
			t.Fatalf("ожидалась ErrTaskNotFound, получено: %v", err)
		}
	})

	t.Run("несуществующая задача", func(t *testing.T) {
		env := newTestEnv(t)

		_, err := env.service.GetTask(t.Context(), app.GetTaskQuery{
			TaskID: todotest.MustTaskID(t).String(), OwnerID: testOwner,
		})
		if !errors.Is(err, app.ErrTaskNotFound) {
			t.Fatalf("ожидалась ErrTaskNotFound, получено: %v", err)
		}
	})

	t.Run("некорректные идентификаторы", func(t *testing.T) {
		cases := []struct {
			name    string
			query   app.GetTaskQuery
			wantErr error
		}{
			{
				name:    "пустой идентификатор задачи",
				query:   app.GetTaskQuery{TaskID: "", OwnerID: testOwner},
				wantErr: todo.ErrInvalidTaskID,
			},
			{
				name:    "пустой владелец",
				query:   app.GetTaskQuery{TaskID: todotest.MustTaskID(t).String(), OwnerID: "  "},
				wantErr: todo.ErrInvalidOwnerID,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				env := newTestEnv(t)

				if _, err := env.service.GetTask(t.Context(), tc.query); !errors.Is(err, tc.wantErr) {
					t.Fatalf("ожидалась %v, получено: %v", tc.wantErr, err)
				}
			})
		}
	})

	t.Run("отменённый запрос", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := env.service.GetTask(ctx, app.GetTaskQuery{
			TaskID: task.ID().String(), OwnerID: testOwner,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась context.Canceled, получено: %v", err)
		}
	})
}
