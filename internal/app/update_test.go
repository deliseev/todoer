package app_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

func TestUpdateTask(t *testing.T) {
	t.Run("несколько полей меняются одной записью", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)
		env.clock.set(testLater)
		newDue := testDue.Add(48 * time.Hour)

		err := env.service.UpdateTask(t.Context(), app.UpdateTaskCommand{
			TaskID:      task.ID().String(),
			OwnerID:     testOwner,
			Title:       new("Купить кефир"),
			Description: new("Литр, любой жирности"),
			Priority:    new("critical"),
			DueDate:     &app.DueDateUpdate{At: &newDue},
		})
		if err != nil {
			t.Fatalf("UpdateTask(...) вернул ошибку: %v", err)
		}

		// Одна запись на всю команду: ради этого хранилище и разрешает
		// накапливать мутации между Get и Save.
		if env.repo.saveCount() != 1 {
			t.Errorf("записей %d, ожидалась 1", env.repo.saveCount())
		}

		snapshot, ok := env.repo.stored(task.ID())
		if !ok {
			t.Fatal("задача исчезла из хранилища")
		}
		if snapshot.Title.String() != "Купить кефир" {
			t.Errorf("заголовок = %q, ожидался %q", snapshot.Title, "Купить кефир")
		}
		if snapshot.Description.String() != "Литр, любой жирности" {
			t.Errorf("описание = %q, ожидалось %q", snapshot.Description, "Литр, любой жирности")
		}
		if snapshot.Priority != todo.PriorityCritical {
			t.Errorf("приоритет = %s, ожидался %s", snapshot.Priority, todo.PriorityCritical)
		}
		if snapshot.DueDate == nil || !snapshot.DueDate.Time().Equal(newDue) {
			t.Errorf("срок = %v, ожидался %s", snapshot.DueDate, newDue)
		}
		if !snapshot.UpdatedAt.Equal(testLater) {
			t.Errorf("updatedAt = %s, ожидалось %s", snapshot.UpdatedAt, testLater)
		}
		// Версия растёт на каждую мутацию, а не на команду: домен считает
		// изменения, а не запросы.
		if want := task.Version() + 4; snapshot.Version != want {
			t.Errorf("версия = %d, ожидалась %d", snapshot.Version, want)
		}
	})

	t.Run("публикуются события всех применённых изменений", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.UpdateTask(t.Context(), app.UpdateTaskCommand{
			TaskID:   task.ID().String(),
			OwnerID:  testOwner,
			Title:    new("Купить кефир"),
			Priority: new("high"),
		})
		if err != nil {
			t.Fatalf("UpdateTask(...) вернул ошибку: %v", err)
		}

		want := []string{todo.EventTaskRenamed, todo.EventTaskPriorityChanged}
		got := env.publisher.published()
		if len(got) != len(want) {
			t.Fatalf("опубликованы события %v, ожидались %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("опубликованы события %v, ожидались %v", got, want)
			}
		}
		if env.publisher.callCount() != 1 {
			t.Errorf("публикатор вызван %d раз, ожидался 1", env.publisher.callCount())
		}
	})

	t.Run("незаданные поля не трогаются", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.UpdateTask(t.Context(), app.UpdateTaskCommand{
			TaskID:  task.ID().String(),
			OwnerID: testOwner,
			Title:   new("Купить кефир"),
		})
		if err != nil {
			t.Fatalf("UpdateTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(task.ID())
		if snapshot.Description != task.Description() {
			t.Errorf("описание = %q, ожидалось прежнее %q", snapshot.Description, task.Description())
		}
		if snapshot.Priority != task.Priority() {
			t.Errorf("приоритет = %s, ожидался прежний %s", snapshot.Priority, task.Priority())
		}
		if snapshot.DueDate == nil {
			t.Error("срок снят, хотя его не трогали")
		}
		if want := task.Version() + 1; snapshot.Version != want {
			t.Errorf("версия = %d, ожидалась %d", snapshot.Version, want)
		}
	})

	t.Run("пустое описание стирает описание", func(t *testing.T) {
		// Указатель на пустую строку — это «поставить пустое», в отличие
		// от nil, который означает «не трогать».
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.UpdateTask(t.Context(), app.UpdateTaskCommand{
			TaskID:      task.ID().String(),
			OwnerID:     testOwner,
			Description: new(""),
		})
		if err != nil {
			t.Fatalf("UpdateTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(task.ID())
		if !snapshot.Description.IsEmpty() {
			t.Errorf("описание = %q, ожидалось пустое", snapshot.Description)
		}
	})

	t.Run("срок снимается пустым DueDateUpdate", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.UpdateTask(t.Context(), app.UpdateTaskCommand{
			TaskID:  task.ID().String(),
			OwnerID: testOwner,
			DueDate: &app.DueDateUpdate{},
		})
		if err != nil {
			t.Fatalf("UpdateTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(task.ID())
		if snapshot.DueDate != nil {
			t.Errorf("срок = %s, ожидалось снятие", snapshot.DueDate.Time())
		}
	})

	t.Run("команда без полей отвергается", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.UpdateTask(t.Context(), app.UpdateTaskCommand{
			TaskID:  task.ID().String(),
			OwnerID: testOwner,
		})
		if !errors.Is(err, app.ErrEmptyUpdate) {
			t.Fatalf("ожидалась ErrEmptyUpdate, получено: %v", err)
		}
		if env.repo.saveCount() != 0 {
			t.Error("пустая команда дошла до записи")
		}
		if env.publisher.callCount() != 0 {
			t.Error("пустая команда породила публикацию")
		}
	})

	t.Run("некорректные поля не доходят до хранилища", func(t *testing.T) {
		cases := []struct {
			name    string
			mutate  func(*app.UpdateTaskCommand)
			wantErr error
		}{
			{
				name:    "пустой заголовок",
				mutate:  func(c *app.UpdateTaskCommand) { c.Title = new("   ") },
				wantErr: todo.ErrEmptyTitle,
			},
			{
				name: "слишком длинное описание",
				mutate: func(c *app.UpdateTaskCommand) {
					c.Description = new(strings.Repeat("я", todo.MaxDescriptionLength+1))
				},
				wantErr: todo.ErrDescriptionTooLong,
			},
			{
				name:    "неизвестный приоритет",
				mutate:  func(c *app.UpdateTaskCommand) { c.Priority = new("urgent") },
				wantErr: todo.ErrUnknownPriority,
			},
			{
				name: "срок в прошлом",
				mutate: func(c *app.UpdateTaskCommand) {
					past := testNow.Add(-time.Hour)
					c.DueDate = &app.DueDateUpdate{At: &past}
				},
				wantErr: todo.ErrDueDateInPast,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				env := newTestEnv(t)
				task := seedTask(t, env.repo, testOwner, todo.StatusPending)

				cmd := app.UpdateTaskCommand{TaskID: task.ID().String(), OwnerID: testOwner}
				tc.mutate(&cmd)

				if err := env.service.UpdateTask(t.Context(), cmd); !errors.Is(err, tc.wantErr) {
					t.Fatalf("ожидалась %v, получено: %v", tc.wantErr, err)
				}
				if env.repo.saveCount() != 0 {
					t.Error("некорректная команда дошла до записи")
				}
			})
		}
	})

	t.Run("отказ на втором поле не сохраняет первое", func(t *testing.T) {
		// Мутации копятся в агрегате и уезжают в хранилище одной записью,
		// поэтому отказ посреди команды не оставляет половину применённой.
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusCompleted)

		err := env.service.UpdateTask(t.Context(), app.UpdateTaskCommand{
			TaskID:   task.ID().String(),
			OwnerID:  testOwner,
			Title:    new("Купить кефир"),
			Priority: new("high"),
		})
		if !errors.Is(err, todo.ErrTaskAlreadyCompleted) {
			t.Fatalf("ожидалась ErrTaskAlreadyCompleted, получено: %v", err)
		}
		if env.repo.saveCount() != 0 {
			t.Error("отказанная команда дошла до записи")
		}

		snapshot, _ := env.repo.stored(task.ID())
		if snapshot.Title != task.Title() {
			t.Errorf("заголовок = %q, ожидался прежний %q", snapshot.Title, task.Title())
		}
	})

	t.Run("чужая задача неотличима от несуществующей", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.UpdateTask(t.Context(), app.UpdateTaskCommand{
			TaskID:  task.ID().String(),
			OwnerID: otherOwner,
			Title:   new("Купить кефир"),
		})
		if !errors.Is(err, app.ErrTaskNotFound) {
			t.Fatalf("ожидалась ErrTaskNotFound, получено: %v", err)
		}
		if env.repo.saveCount() != 0 {
			t.Error("чужая команда дошла до записи")
		}
	})

	t.Run("форма без правок не двигает версию и молчит", func(t *testing.T) {
		// Клиент прочитал задачу через GetTask и вернул форму нетронутой.
		// Это самый частый вид запроса у whole-form сценария, и изменением
		// он не является: ни версия, ни подписчики о нём знать не должны.
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)
		env.clock.set(testLater)

		before, ok := env.repo.stored(task.ID())
		if !ok {
			t.Fatal("задача исчезла из хранилища")
		}
		sameDue := before.DueDate.Time()

		err := env.service.UpdateTask(t.Context(), app.UpdateTaskCommand{
			TaskID:      task.ID().String(),
			OwnerID:     testOwner,
			Title:       new(before.Title.String()),
			Description: new(before.Description.String()),
			Priority:    new(before.Priority.String()),
			DueDate:     &app.DueDateUpdate{At: &sameDue},
		})
		if err != nil {
			t.Fatalf("UpdateTask(...) вернул ошибку: %v", err)
		}

		after, ok := env.repo.stored(task.ID())
		if !ok {
			t.Fatal("задача исчезла из хранилища")
		}
		if after.Version != before.Version {
			t.Errorf("версия = %d, ожидалась %d", after.Version, before.Version)
		}
		if !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Errorf("updatedAt = %s, ожидалось %s", after.UpdatedAt, before.UpdatedAt)
		}
		if published := env.publisher.published(); len(published) != 0 {
			t.Errorf("опубликовано %v, ожидалось пусто", published)
		}
	})
}
