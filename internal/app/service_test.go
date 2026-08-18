package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

func TestNewTaskService(t *testing.T) {
	repo := newFakeRepository()
	uow := newFakeUnitOfWork(repo, newFakeOutbox())
	clock := &stubClock{at: testNow}

	t.Run("все зависимости на месте", func(t *testing.T) {
		service, err := app.NewTaskService(uow, repo, clock)
		if err != nil {
			t.Fatalf("NewTaskService(...) вернул ошибку: %v", err)
		}
		if service == nil {
			t.Fatal("NewTaskService(...) вернул nil без ошибки")
		}
	})

	t.Run("единица работы обязательна", func(t *testing.T) {
		// Nil вместо неё — изменения без атомарности: задача запишется, а
		// событие о ней потеряется, и никто об этом не узнает.
		if _, err := app.NewTaskService(nil, repo, clock); !errors.Is(err, app.ErrMissingDependency) {
			t.Fatalf("ожидалась ErrMissingDependency, получено: %v", err)
		}
	})

	t.Run("хранилище обязательно", func(t *testing.T) {
		if _, err := app.NewTaskService(uow, nil, clock); !errors.Is(err, app.ErrMissingDependency) {
			t.Fatalf("ожидалась ErrMissingDependency, получено: %v", err)
		}
	})

	t.Run("часы обязательны", func(t *testing.T) {
		if _, err := app.NewTaskService(uow, repo, nil); !errors.Is(err, app.ErrMissingDependency) {
			t.Fatalf("ожидалась ErrMissingDependency, получено: %v", err)
		}
	})
}

func TestCreateTask(t *testing.T) {
	validCommand := func() app.CreateTaskCommand {
		return app.CreateTaskCommand{
			OwnerID:     testOwner,
			Title:       "Купить молоко",
			Description: "Два литра, в магазине у дома",
			Priority:    "high",
			DueDate:     &testDue,
		}
	}

	t.Run("задача сохраняется в первой версии", func(t *testing.T) {
		env := newTestEnv(t)

		id, err := env.service.CreateTask(t.Context(), validCommand())
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		snapshot, ok := env.repo.stored(id)
		if !ok {
			t.Fatal("задача не попала в хранилище")
		}
		if snapshot.Version != 1 {
			t.Errorf("версия = %d, ожидалась 1", snapshot.Version)
		}
		if snapshot.Status != todo.StatusPending {
			t.Errorf("статус = %s, ожидался %s", snapshot.Status, todo.StatusPending)
		}
		if snapshot.OwnerID.String() != testOwner {
			t.Errorf("владелец = %q, ожидался %q", snapshot.OwnerID, testOwner)
		}
		if snapshot.Title.String() != "Купить молоко" {
			t.Errorf("заголовок = %q, ожидался %q", snapshot.Title, "Купить молоко")
		}
		if snapshot.Priority != todo.PriorityHigh {
			t.Errorf("приоритет = %s, ожидался %s", snapshot.Priority, todo.PriorityHigh)
		}
		if snapshot.DueDate == nil {
			t.Fatal("срок не сохранён")
		}
		if !snapshot.DueDate.Time().Equal(testDue) {
			t.Errorf("срок = %s, ожидался %s", snapshot.DueDate.Time(), testDue)
		}
	})

	t.Run("идентификатор задачи выдаётся вызывающему", func(t *testing.T) {
		env := newTestEnv(t)

		id, err := env.service.CreateTask(t.Context(), validCommand())
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}
		if id.IsZero() {
			t.Fatal("возвращён нулевой идентификатор")
		}
	})

	t.Run("время берётся из часов сервиса", func(t *testing.T) {
		env := newTestEnv(t)
		env.clock.set(testLater)

		id, err := env.service.CreateTask(t.Context(), validCommand())
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(id)
		// Сравнивать моменты времени только через Equal: == у time.Time
		// сличает внутреннее представление, а не момент.
		if !snapshot.CreatedAt.Equal(testLater) {
			t.Errorf("createdAt = %s, ожидалось %s", snapshot.CreatedAt, testLater)
		}
		if !snapshot.UpdatedAt.Equal(testLater) {
			t.Errorf("updatedAt = %s, ожидалось %s", snapshot.UpdatedAt, testLater)
		}
	})

	t.Run("пустой приоритет означает обычный", func(t *testing.T) {
		env := newTestEnv(t)

		cmd := validCommand()
		cmd.Priority = ""

		id, err := env.service.CreateTask(t.Context(), cmd)
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(id)
		if snapshot.Priority != todo.PriorityNormal {
			t.Errorf("приоритет = %s, ожидался %s", snapshot.Priority, todo.PriorityNormal)
		}
	})

	t.Run("приоритет разбирается без учёта регистра", func(t *testing.T) {
		env := newTestEnv(t)

		cmd := validCommand()
		cmd.Priority = "  CRITICAL "

		id, err := env.service.CreateTask(t.Context(), cmd)
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(id)
		if snapshot.Priority != todo.PriorityCritical {
			t.Errorf("приоритет = %s, ожидался %s", snapshot.Priority, todo.PriorityCritical)
		}
	})

	t.Run("задача без срока создаётся", func(t *testing.T) {
		env := newTestEnv(t)

		cmd := validCommand()
		cmd.DueDate = nil

		id, err := env.service.CreateTask(t.Context(), cmd)
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(id)
		if snapshot.DueDate != nil {
			t.Errorf("срок = %s, ожидалось отсутствие срока", snapshot.DueDate.Time())
		}
	})

	t.Run("публикуется событие создания", func(t *testing.T) {
		env := newTestEnv(t)

		id, err := env.service.CreateTask(t.Context(), validCommand())
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		if got := env.outbox.queued(); len(got) != 1 || got[0] != todo.EventTaskCreated {
			t.Fatalf("опубликованы события %v, ожидалось [%s]", got, todo.EventTaskCreated)
		}

		created, ok := env.outbox.eventAt(0).(todo.TaskCreated)
		if !ok {
			t.Fatalf("тип события %T, ожидался todo.TaskCreated", env.outbox.eventAt(0))
		}
		if created.AggregateID() != id {
			t.Errorf("событие о задаче %s, ожидалась %s", created.AggregateID(), id)
		}
		if created.OwnerID.String() != testOwner {
			t.Errorf("владелец в событии = %q, ожидался %q", created.OwnerID, testOwner)
		}
	})

	t.Run("несохранённая задача событий не порождает", func(t *testing.T) {
		env := newTestEnv(t)
		saveErr := errors.New("хранилище недоступно")
		env.repo.failSave(saveErr)

		_, err := env.service.CreateTask(t.Context(), validCommand())
		if !errors.Is(err, saveErr) {
			t.Fatalf("ожидалась ошибка хранилища, получено: %v", err)
		}
		if len(env.outbox.queued()) != 0 {
			t.Errorf("в очереди %d событий, ожидалось 0", len(env.outbox.queued()))
		}
	})

	t.Run("отказ очереди откатывает задачу", func(t *testing.T) {
		// То, ради чего заведена единица работы. Раньше задача оставалась
		// записанной, а событие о ней терялось, и вызывающему возвращали
		// «создано, но не доставлено». Теперь событие пишется вместе с задачей,
		// поэтому отказ очереди отменяет и её: рассказать об изменении нечем —
		// значит изменения не было.
		env := newTestEnv(t)
		outboxErr := errors.New("очередь недоступна")
		env.outbox.failAdd(outboxErr)

		id, err := env.service.CreateTask(t.Context(), validCommand())
		if !errors.Is(err, outboxErr) {
			t.Fatalf("ожидалась ошибка очереди, получено: %v", err)
		}
		if _, ok := env.repo.stored(id); ok {
			t.Error("задача сохранена, хотя событие о ней записать не удалось")
		}
		if len(env.outbox.queued()) != 0 {
			t.Errorf("в очереди %d событий, ожидалось 0", len(env.outbox.queued()))
		}
	})

	t.Run("некорректный ввод не доходит до хранилища", func(t *testing.T) {
		cases := []struct {
			name    string
			mutate  func(*app.CreateTaskCommand)
			wantErr error
		}{
			{
				name:    "пустой заголовок",
				mutate:  func(c *app.CreateTaskCommand) { c.Title = "   " },
				wantErr: todo.ErrEmptyTitle,
			},
			{
				name:    "слишком длинный заголовок",
				mutate:  func(c *app.CreateTaskCommand) { c.Title = strings.Repeat("я", todo.MaxTitleLength+1) },
				wantErr: todo.ErrTitleTooLong,
			},
			{
				name:    "слишком длинное описание",
				mutate:  func(c *app.CreateTaskCommand) { c.Description = strings.Repeat("я", todo.MaxDescriptionLength+1) },
				wantErr: todo.ErrDescriptionTooLong,
			},
			{
				name:    "пустой владелец",
				mutate:  func(c *app.CreateTaskCommand) { c.OwnerID = "  " },
				wantErr: todo.ErrInvalidOwnerID,
			},
			{
				name:    "неизвестный приоритет",
				mutate:  func(c *app.CreateTaskCommand) { c.Priority = "urgent" },
				wantErr: todo.ErrUnknownPriority,
			},
			{
				name: "срок в прошлом",
				mutate: func(c *app.CreateTaskCommand) {
					past := testNow.Add(-time.Hour)
					c.DueDate = &past
				},
				wantErr: todo.ErrDueDateInPast,
			},
			{
				name: "срок совпадает с текущим моментом",
				mutate: func(c *app.CreateTaskCommand) {
					now := testNow
					c.DueDate = &now
				},
				wantErr: todo.ErrDueDateInPast,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				env := newTestEnv(t)

				cmd := validCommand()
				tc.mutate(&cmd)

				_, err := env.service.CreateTask(t.Context(), cmd)
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ожидалась %v, получено: %v", tc.wantErr, err)
				}
				if env.repo.saveCount() != 0 {
					t.Error("некорректная команда дошла до хранилища")
				}
				if len(env.outbox.queued()) != 0 {
					t.Error("некорректная команда породила публикацию")
				}
			})
		}
	})
}

// taskMutation — сценарий, меняющий уже существующую задачу.
//
// Таблица перечисляет такие сценарии один раз, и её переиспользуют все тесты,
// обходящие операции: авторизация, отсутствие задачи, разбор идентификаторов,
// поведение на терминальном статусе. Добавляя сценарий в TaskService,
// добавить его сюда — тогда он автоматически попадёт под эти проверки.
type taskMutation struct {
	name  string
	event string
	run   func(ctx context.Context, s *app.TaskService, taskID, ownerID string) error
}

func taskMutations() []taskMutation {
	return []taskMutation{
		{
			name:  "RenameTask",
			event: todo.EventTaskRenamed,
			run: func(ctx context.Context, s *app.TaskService, taskID, ownerID string) error {
				return s.RenameTask(ctx, app.RenameTaskCommand{
					TaskID: taskID, OwnerID: ownerID, Title: "Купить кефир",
				})
			},
		},
		{
			// В таблице участвует с одним полем: общие проверки — про
			// авторизацию, отмену и конфликт версий, а не про число мутаций.
			name:  "UpdateTask",
			event: todo.EventTaskRenamed,
			run: func(ctx context.Context, s *app.TaskService, taskID, ownerID string) error {
				return s.UpdateTask(ctx, app.UpdateTaskCommand{
					TaskID: taskID, OwnerID: ownerID, Title: new("Купить кефир"),
				})
			},
		},
		{
			name:  "DescribeTask",
			event: todo.EventTaskDescribed,
			run: func(ctx context.Context, s *app.TaskService, taskID, ownerID string) error {
				return s.DescribeTask(ctx, app.DescribeTaskCommand{
					TaskID: taskID, OwnerID: ownerID, Description: "Литр, любой жирности",
				})
			},
		},
		{
			name:  "ChangePriority",
			event: todo.EventTaskPriorityChanged,
			run: func(ctx context.Context, s *app.TaskService, taskID, ownerID string) error {
				return s.ChangePriority(ctx, app.ChangePriorityCommand{
					TaskID: taskID, OwnerID: ownerID, Priority: "critical",
				})
			},
		},
		{
			name:  "RescheduleTask",
			event: todo.EventTaskRescheduled,
			run: func(ctx context.Context, s *app.TaskService, taskID, ownerID string) error {
				at := testDue.Add(48 * time.Hour)
				return s.RescheduleTask(ctx, app.RescheduleTaskCommand{
					TaskID: taskID, OwnerID: ownerID, DueDate: &at,
				})
			},
		},
		{
			name:  "StartTask",
			event: todo.EventTaskStarted,
			run: func(ctx context.Context, s *app.TaskService, taskID, ownerID string) error {
				return s.StartTask(ctx, app.StartTaskCommand{TaskID: taskID, OwnerID: ownerID})
			},
		},
		{
			name:  "CompleteTask",
			event: todo.EventTaskCompleted,
			run: func(ctx context.Context, s *app.TaskService, taskID, ownerID string) error {
				return s.CompleteTask(ctx, app.CompleteTaskCommand{TaskID: taskID, OwnerID: ownerID})
			},
		},
		{
			name:  "CancelTask",
			event: todo.EventTaskCancelled,
			run: func(ctx context.Context, s *app.TaskService, taskID, ownerID string) error {
				return s.CancelTask(ctx, app.CancelTaskCommand{TaskID: taskID, OwnerID: ownerID})
			},
		},
	}
}

func TestTaskMutationsSuccess(t *testing.T) {
	for _, m := range taskMutations() {
		t.Run(m.name, func(t *testing.T) {
			env := newTestEnv(t)
			task := seedTask(t, env.repo, testOwner, todo.StatusPending)
			env.clock.set(testLater)

			if err := m.run(t.Context(), env.service, task.ID().String(), testOwner); err != nil {
				t.Fatalf("%s(...) вернул ошибку: %v", m.name, err)
			}

			snapshot, ok := env.repo.stored(task.ID())
			if !ok {
				t.Fatal("задача исчезла из хранилища")
			}
			if snapshot.Version != task.Version()+1 {
				t.Errorf("версия = %d, ожидалась %d", snapshot.Version, task.Version()+1)
			}
			if !snapshot.UpdatedAt.Equal(testLater) {
				t.Errorf("updatedAt = %s, ожидалось %s", snapshot.UpdatedAt, testLater)
			}
			if got := env.outbox.queued(); len(got) != 1 || got[0] != m.event {
				t.Errorf("опубликованы события %v, ожидалось [%s]", got, m.event)
			}
		})
	}
}

func TestTaskMutationsWithoutChangeSkipWrite(t *testing.T) {
	// Домен на повторе не двигает версию, и сценарию писать нечего: снимок
	// в хранилище уже такой. Лишняя запись не портит данные, но подставляет
	// вызывающего под ErrVersionConflict за чужую запись, случившуюся между
	// Get и Save, — за изменение, которого он не делал.
	t.Run("тот же заголовок не доходит до записи", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)
		env.clock.set(testLater)

		before, ok := env.repo.stored(task.ID())
		if !ok {
			t.Fatal("задача исчезла из хранилища")
		}

		err := env.service.RenameTask(t.Context(), app.RenameTaskCommand{
			TaskID:  task.ID().String(),
			OwnerID: testOwner,
			Title:   before.Title.String(),
		})
		if err != nil {
			t.Fatalf("RenameTask(...) вернул ошибку: %v", err)
		}

		if got := env.repo.saveCount(); got != 0 {
			t.Errorf("записей %d, ожидалось 0", got)
		}
		if got := len(env.outbox.queued()); got != 0 {
			t.Errorf("обращений к публикатору %d, ожидалось 0", got)
		}
	})

	t.Run("чужая запись между Get и Save не мешает пустой команде", func(t *testing.T) {
		// Раз писать нечего, то и конфликтовать не с чем: команда, ничего
		// не меняющая, обязана пережить параллельного редактора.
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)
		env.clock.set(testLater)

		before, ok := env.repo.stored(task.ID())
		if !ok {
			t.Fatal("задача исчезла из хранилища")
		}

		// Хук изображает соседа, записавшего свою правку между Get и Save.
		// Если сценарий всё-таки полезет писать, он получит конфликт версий
		// за изменение, которого не делал.
		env.repo.onBeforeSave(func() {
			rival, err := todo.ReconstituteTask(before)
			if err != nil {
				t.Errorf("ReconstituteTask(...) вернул ошибку: %v", err)
				return
			}
			if err := rival.Rename(todotest.MustTitle(t, "Версия соседа"), testLater); err != nil {
				t.Errorf("Rename(...) вернул ошибку: %v", err)
				return
			}
			env.repo.put(rival.Snapshot())
		})

		err := env.service.RenameTask(t.Context(), app.RenameTaskCommand{
			TaskID:  task.ID().String(),
			OwnerID: testOwner,
			Title:   before.Title.String(),
		})
		if err != nil {
			t.Fatalf("RenameTask(...) вернул ошибку: %v", err)
		}
	})
}

func TestTaskMutationsRejectForeignOwner(t *testing.T) {
	for _, m := range taskMutations() {
		t.Run(m.name, func(t *testing.T) {
			env := newTestEnv(t)
			task := seedTask(t, env.repo, testOwner, todo.StatusPending)

			// Для постороннего чужой задачи не существует, и отличаться от
			// по-настоящему несуществующей она не должна ничем, что можно
			// проверить программой: иначе перебор идентификаторов
			// рассказывает, какие из них заняты.
			err := m.run(t.Context(), env.service, task.ID().String(), otherOwner)
			if !errors.Is(err, app.ErrTaskNotFound) {
				t.Fatalf("ожидалась ErrTaskNotFound, получено: %v", err)
			}
			if env.repo.saveCount() != 0 {
				t.Error("чужая команда дошла до хранилища")
			}
			if len(env.outbox.queued()) != 0 {
				t.Error("чужая команда породила публикацию")
			}
		})
	}
}

func TestTaskMutationsOnMissingTask(t *testing.T) {
	for _, m := range taskMutations() {
		t.Run(m.name, func(t *testing.T) {
			env := newTestEnv(t)
			missing := todotest.MustTaskID(t)

			err := m.run(t.Context(), env.service, missing.String(), testOwner)
			if !errors.Is(err, app.ErrTaskNotFound) {
				t.Fatalf("ожидалась ErrTaskNotFound, получено: %v", err)
			}
			if len(env.outbox.queued()) != 0 {
				t.Error("отсутствующая задача породила публикацию")
			}
		})
	}
}

func TestTaskMutationsRejectMalformedIdentifiers(t *testing.T) {
	cases := []struct {
		name string
		// existingTaskID берёт идентификатор реально существующей задачи —
		// тогда единственное, что не так в команде, это владелец.
		existingTaskID bool
		taskID         string
		ownerID        string
		wantErr        error
	}{
		{
			name:    "пустой идентификатор задачи",
			taskID:  "",
			ownerID: testOwner,
			wantErr: todo.ErrInvalidTaskID,
		},
		{
			name:    "идентификатор задачи не hex",
			taskID:  strings.Repeat("z", todo.TaskIDLength),
			ownerID: testOwner,
			wantErr: todo.ErrInvalidTaskID,
		},
		{
			name:           "пустой владелец",
			existingTaskID: true,
			ownerID:        "   ",
			wantErr:        todo.ErrInvalidOwnerID,
		},
	}

	for _, m := range taskMutations() {
		t.Run(m.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					env := newTestEnv(t)
					task := seedTask(t, env.repo, testOwner, todo.StatusPending)

					taskID := tc.taskID
					if tc.existingTaskID {
						taskID = task.ID().String()
					}

					err := m.run(t.Context(), env.service, taskID, tc.ownerID)
					if !errors.Is(err, tc.wantErr) {
						t.Fatalf("ожидалась %v, получено: %v", tc.wantErr, err)
					}
					if env.repo.saveCount() != 0 {
						t.Error("некорректная команда дошла до хранилища")
					}
				})
			}
		})
	}
}

func TestTaskMutationsOnTerminalTask(t *testing.T) {
	terminal := []struct {
		status  todo.Status
		wantErr error
	}{
		{status: todo.StatusCompleted, wantErr: todo.ErrTaskAlreadyCompleted},
		{status: todo.StatusCancelled, wantErr: todo.ErrTaskCancelled},
	}

	for _, m := range taskMutations() {
		t.Run(m.name, func(t *testing.T) {
			for _, tc := range terminal {
				t.Run(tc.status.String(), func(t *testing.T) {
					env := newTestEnv(t)
					task := seedTask(t, env.repo, testOwner, tc.status)

					err := m.run(t.Context(), env.service, task.ID().String(), testOwner)
					if !errors.Is(err, tc.wantErr) {
						t.Fatalf("ожидалась %v, получено: %v", tc.wantErr, err)
					}
					if env.repo.saveCount() != 0 {
						t.Error("отказанная доменом команда дошла до хранилища")
					}
					if len(env.outbox.queued()) != 0 {
						t.Error("отказанная доменом команда породила публикацию")
					}
				})
			}
		})
	}
}

func TestTaskMutationsReportStorageFailure(t *testing.T) {
	for _, m := range taskMutations() {
		t.Run(m.name, func(t *testing.T) {
			env := newTestEnv(t)
			task := seedTask(t, env.repo, testOwner, todo.StatusPending)
			getErr := errors.New("хранилище недоступно")
			env.repo.failGet(getErr)

			// Отказ чтения — не «задачи нет»: подменять его ErrTaskNotFound
			// значило бы сказать вызывающему, что задача удалена, хотя про
			// неё попросту ничего не известно.
			err := m.run(t.Context(), env.service, task.ID().String(), testOwner)
			if !errors.Is(err, getErr) {
				t.Fatalf("ожидалась ошибка хранилища, получено: %v", err)
			}
			if errors.Is(err, app.ErrTaskNotFound) {
				t.Error("отказ чтения выдан за отсутствие задачи")
			}
			if env.repo.saveCount() != 0 {
				t.Error("непрочитанная задача дошла до записи")
			}
			if len(env.outbox.queued()) != 0 {
				t.Error("непрочитанная задача породила событие")
			}
		})
	}
}

func TestTaskMutationsRollBackOnOutboxFailure(t *testing.T) {
	for _, m := range taskMutations() {
		t.Run(m.name, func(t *testing.T) {
			// Раньше отказ доставки оставлял изменение записанным и возвращал
			// его вызывающему отдельной ошибкой. Теперь событие пишется вместе
			// с задачей, и отказ очереди отменяет изменение целиком: состояния
			// «изменено, но никто не узнает» больше не существует.
			env := newTestEnv(t)
			task := seedTask(t, env.repo, testOwner, todo.StatusPending)
			outboxErr := errors.New("очередь недоступна")
			env.outbox.failAdd(outboxErr)

			err := m.run(t.Context(), env.service, task.ID().String(), testOwner)
			if !errors.Is(err, outboxErr) {
				t.Fatalf("ожидалась ошибка очереди, получено: %v", err)
			}

			snapshot, ok := env.repo.stored(task.ID())
			if !ok {
				t.Fatal("задача исчезла из хранилища")
			}
			if snapshot.Version != task.Version() {
				t.Errorf("версия = %d, ожидалась %d — изменение не откатилось",
					snapshot.Version, task.Version())
			}
			if len(env.outbox.queued()) != 0 {
				t.Errorf("в очереди %d событий, ожидалось 0", len(env.outbox.queued()))
			}
		})
	}
}

func TestTaskMutationsReportVersionConflict(t *testing.T) {
	// Чужая запись, вклинившаяся между Get и Save, обязана дойти до
	// вызывающего конфликтом версий: молчаливая перезапись потеряла бы
	// чужое изменение, а вызывающий уверился бы, что его правка легла
	// поверх актуальной задачи. Порт это обещает, и обещание проверяется
	// на каждой мутации разом — иначе новый сценарий, написанный мимо
	// mutate, потерял бы проверку и никто бы не заметил.
	for _, m := range taskMutations() {
		t.Run(m.name, func(t *testing.T) {
			env := newTestEnv(t)
			task := seedTask(t, env.repo, testOwner, todo.StatusPending)

			// Пока сценарий держал задачу в руках, её изменил кто-то ещё.
			env.repo.onBeforeSave(func() {
				stale := task.Snapshot()
				stale.Version = task.Version() + 5
				env.repo.put(stale)
			})

			err := m.run(t.Context(), env.service, task.ID().String(), testOwner)

			if !errors.Is(err, app.ErrVersionConflict) {
				t.Fatalf("ожидалась ErrVersionConflict, получено: %v", err)
			}
			if got := env.outbox.queued(); len(got) != 0 {
				t.Errorf("в очереди %d событий, ожидалось 0: несохранённая мутация о себе рассказала", len(got))
			}
		})
	}
}

func TestTaskMutationsRollBackOnCancellation(t *testing.T) {
	for _, m := range taskMutations() {
		t.Run(m.name, func(t *testing.T) {
			// Клиент отвалился ровно в момент записи. До фиксации отмена
			// законна и работу останавливает — вместе с задачей откатывается
			// и событие о ней.
			//
			// Прежде было наоборот: задача сохранялась, а доставка шла с
			// контекстом без отмены, потому что рассказать об уже случившемся
			// изменении надо было независимо от клиента. С очередью эта забота
			// исчезла: рассказ пишется той же транзакцией, что и изменение.
			env := newTestEnv(t)
			task := seedTask(t, env.repo, testOwner, todo.StatusPending)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			env.repo.onBeforeSave(cancel)

			err := m.run(ctx, env.service, task.ID().String(), testOwner)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("ожидалась context.Canceled, получено: %v", err)
			}

			snapshot, ok := env.repo.stored(task.ID())
			if !ok {
				t.Fatal("задача исчезла из хранилища")
			}
			if snapshot.Version != task.Version() {
				t.Errorf("версия = %d, ожидалась %d — отменённый запрос всё-таки записал изменение",
					snapshot.Version, task.Version())
			}
			if len(env.outbox.queued()) != 0 {
				t.Errorf("в очереди %d событий, ожидалось 0", len(env.outbox.queued()))
			}
		})
	}
}

func TestTaskMutationsRejectCancelledRequest(t *testing.T) {
	for _, m := range taskMutations() {
		t.Run(m.name, func(t *testing.T) {
			env := newTestEnv(t)
			task := seedTask(t, env.repo, testOwner, todo.StatusPending)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			// До записи отмена законна и должна останавливать работу:
			// хранилище обязано отказать, а сценарий — не сделать вид,
			// будто ничего не просили.
			err := m.run(ctx, env.service, task.ID().String(), testOwner)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("ожидалась context.Canceled, получено: %v", err)
			}
			if env.repo.saveCount() != 0 {
				t.Error("отменённый запрос дошёл до записи")
			}
			if len(env.outbox.queued()) != 0 {
				t.Error("отменённый запрос породил публикацию")
			}
		})
	}
}

func TestRenameTask(t *testing.T) {
	t.Run("заголовок нормализуется", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.RenameTask(t.Context(), app.RenameTaskCommand{
			TaskID: task.ID().String(), OwnerID: testOwner, Title: "  Купить кефир  ",
		})
		if err != nil {
			t.Fatalf("RenameTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(task.ID())
		if snapshot.Title.String() != "Купить кефир" {
			t.Errorf("заголовок = %q, ожидался %q", snapshot.Title, "Купить кефир")
		}
	})

	t.Run("пустой заголовок отвергается", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.RenameTask(t.Context(), app.RenameTaskCommand{
			TaskID: task.ID().String(), OwnerID: testOwner, Title: "   ",
		})
		if !errors.Is(err, todo.ErrEmptyTitle) {
			t.Fatalf("ожидалась ErrEmptyTitle, получено: %v", err)
		}
		if env.repo.saveCount() != 0 {
			t.Error("пустой заголовок дошёл до хранилища")
		}
	})
}

func TestDescribeTask(t *testing.T) {
	t.Run("пустое описание допустимо", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.DescribeTask(t.Context(), app.DescribeTaskCommand{
			TaskID: task.ID().String(), OwnerID: testOwner, Description: "",
		})
		if err != nil {
			t.Fatalf("DescribeTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(task.ID())
		if !snapshot.Description.IsEmpty() {
			t.Errorf("описание = %q, ожидалось пустое", snapshot.Description)
		}
	})

	t.Run("слишком длинное описание отвергается", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.DescribeTask(t.Context(), app.DescribeTaskCommand{
			TaskID:      task.ID().String(),
			OwnerID:     testOwner,
			Description: strings.Repeat("я", todo.MaxDescriptionLength+1),
		})
		if !errors.Is(err, todo.ErrDescriptionTooLong) {
			t.Fatalf("ожидалась ErrDescriptionTooLong, получено: %v", err)
		}
	})
}

func TestChangePriority(t *testing.T) {
	t.Run("неизвестный приоритет отвергается", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.ChangePriority(t.Context(), app.ChangePriorityCommand{
			TaskID: task.ID().String(), OwnerID: testOwner, Priority: "urgent",
		})
		if !errors.Is(err, todo.ErrUnknownPriority) {
			t.Fatalf("ожидалась ErrUnknownPriority, получено: %v", err)
		}
		if env.repo.saveCount() != 0 {
			t.Error("неизвестный приоритет дошёл до хранилища")
		}
	})

	t.Run("пустой приоритет отвергается", func(t *testing.T) {
		// В отличие от создания, здесь пустая строка — не «оставить как есть»,
		// а команда без содержания: менять приоритет неизвестно на какой.
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.ChangePriority(t.Context(), app.ChangePriorityCommand{
			TaskID: task.ID().String(), OwnerID: testOwner, Priority: "",
		})
		if !errors.Is(err, todo.ErrUnknownPriority) {
			t.Fatalf("ожидалась ErrUnknownPriority, получено: %v", err)
		}
	})
}

func TestRescheduleTask(t *testing.T) {
	t.Run("срок переносится", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)
		at := testDue.Add(48 * time.Hour)

		err := env.service.RescheduleTask(t.Context(), app.RescheduleTaskCommand{
			TaskID: task.ID().String(), OwnerID: testOwner, DueDate: &at,
		})
		if err != nil {
			t.Fatalf("RescheduleTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(task.ID())
		if snapshot.DueDate == nil {
			t.Fatal("срок не сохранён")
		}
		if !snapshot.DueDate.Time().Equal(at) {
			t.Errorf("срок = %s, ожидался %s", snapshot.DueDate.Time(), at)
		}
	})

	t.Run("nil снимает срок", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.RescheduleTask(t.Context(), app.RescheduleTaskCommand{
			TaskID: task.ID().String(), OwnerID: testOwner, DueDate: nil,
		})
		if err != nil {
			t.Fatalf("RescheduleTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(task.ID())
		if snapshot.DueDate != nil {
			t.Errorf("срок = %s, ожидалось снятие срока", snapshot.DueDate.Time())
		}
	})

	t.Run("срок проверяется по часам сервиса", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		// Срок был в будущем при постановке задачи, но часы ушли вперёд.
		// Момент именно чужой: собственный срок задачи — повтор, а не
		// назначение, и под эту проверку он не подпадает.
		env.clock.set(testDue.Add(time.Hour))
		stale := testDue.Add(30 * time.Minute)

		err := env.service.RescheduleTask(t.Context(), app.RescheduleTaskCommand{
			TaskID: task.ID().String(), OwnerID: testOwner, DueDate: &stale,
		})
		if !errors.Is(err, todo.ErrDueDateInPast) {
			t.Fatalf("ожидалась ErrDueDateInPast, получено: %v", err)
		}
		if env.repo.saveCount() != 0 {
			t.Error("просроченный срок дошёл до хранилища")
		}
	})
}

func TestTaskStatusScenarios(t *testing.T) {
	t.Run("выполнение проставляет момент завершения", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusInProgress)
		env.clock.set(testLater)

		err := env.service.CompleteTask(t.Context(), app.CompleteTaskCommand{
			TaskID: task.ID().String(), OwnerID: testOwner,
		})
		if err != nil {
			t.Fatalf("CompleteTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(task.ID())
		if snapshot.Status != todo.StatusCompleted {
			t.Errorf("статус = %s, ожидался %s", snapshot.Status, todo.StatusCompleted)
		}
		if snapshot.CompletedAt == nil {
			t.Fatal("момент завершения не сохранён")
		}
		if !snapshot.CompletedAt.Equal(testLater) {
			t.Errorf("completedAt = %s, ожидалось %s", snapshot.CompletedAt, testLater)
		}
	})

	t.Run("повторный запуск задачи в работе отвергается", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusInProgress)

		err := env.service.StartTask(t.Context(), app.StartTaskCommand{
			TaskID: task.ID().String(), OwnerID: testOwner,
		})
		if !errors.Is(err, todo.ErrInvalidStatusTransition) {
			t.Fatalf("ожидалась ErrInvalidStatusTransition, получено: %v", err)
		}
		if env.repo.saveCount() != 0 {
			t.Error("запрещённый переход дошёл до хранилища")
		}
	})

	t.Run("последовательность мутаций копит версию", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)
		id := task.ID().String()
		ctx := t.Context()

		if err := env.service.StartTask(ctx, app.StartTaskCommand{TaskID: id, OwnerID: testOwner}); err != nil {
			t.Fatalf("StartTask(...) вернул ошибку: %v", err)
		}
		if err := env.service.RenameTask(ctx, app.RenameTaskCommand{
			TaskID: id, OwnerID: testOwner, Title: "Купить кефир",
		}); err != nil {
			t.Fatalf("RenameTask(...) вернул ошибку: %v", err)
		}
		if err := env.service.CompleteTask(ctx, app.CompleteTaskCommand{TaskID: id, OwnerID: testOwner}); err != nil {
			t.Fatalf("CompleteTask(...) вернул ошибку: %v", err)
		}

		snapshot, _ := env.repo.stored(task.ID())
		if want := task.Version() + 3; snapshot.Version != want {
			t.Errorf("версия = %d, ожидалась %d", snapshot.Version, want)
		}

		want := []string{todo.EventTaskStarted, todo.EventTaskRenamed, todo.EventTaskCompleted}
		got := env.outbox.queued()
		if len(got) != len(want) {
			t.Fatalf("опубликованы события %v, ожидались %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("опубликованы события %v, ожидались %v", got, want)
			}
		}
	})

	t.Run("очередь не дёргается впустую", func(t *testing.T) {
		env := newTestEnv(t)
		task := seedTask(t, env.repo, testOwner, todo.StatusPending)

		err := env.service.CancelTask(t.Context(), app.CancelTaskCommand{
			TaskID: task.ID().String(), OwnerID: testOwner,
		})
		if err != nil {
			t.Fatalf("CancelTask(...) вернул ошибку: %v", err)
		}
		if env.outbox.sawEmptyBatch() {
			t.Error("очередь получила пустую партию событий")
		}
	})
}
