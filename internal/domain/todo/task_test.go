package todo_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// Моменты времени, которыми пользуются тесты агрегата.
var (
	testLater      = testNow.Add(time.Hour)
	testMuchLater  = testNow.Add(2 * time.Hour)
	testEvenLater  = testNow.Add(3 * time.Hour)
	testTomorrowAt = testNow.Add(24 * time.Hour)
)

// taskArgs — аргументы конструктора NewTask, собранные в одну структуру,
// чтобы тест мог испортить ровно одно поле и проверить реакцию.
type taskArgs struct {
	id          todo.TaskID
	ownerID     todo.OwnerID
	title       todo.Title
	description todo.Description
	priority    todo.Priority
	dueDate     *todo.DueDate
}

// validTaskArgs собирает заведомо корректный набор аргументов.
func validTaskArgs(t *testing.T) taskArgs {
	t.Helper()

	return taskArgs{
		id:          mustTaskID(t),
		ownerID:     mustOwnerID(t, "user-42"),
		title:       mustTitle(t, "Купить молоко"),
		description: mustDescription(t, "Два литра, в магазине у дома"),
		priority:    todo.PriorityHigh,
		dueDate:     mustDueDate(t, testTomorrowAt),
	}
}

// newTask вызывает конструктор с собранными аргументами.
func (a taskArgs) newTask(now time.Time) (*todo.Task, error) {
	return todo.NewTask(a.id, a.ownerID, a.title, a.description, a.priority, a.dueDate, now)
}

func TestNewTask(t *testing.T) {
	t.Parallel()

	args := validTaskArgs(t)

	task, err := args.newTask(testNow)
	if err != nil {
		t.Fatalf("NewTask(...) вернул ошибку: %v", err)
	}

	if task.ID() != args.id {
		t.Errorf("Task.ID() = %q, ожидалось %q", task.ID().String(), args.id.String())
	}
	if task.OwnerID() != args.ownerID {
		t.Errorf("Task.OwnerID() = %q, ожидалось %q", task.OwnerID().String(), args.ownerID.String())
	}
	if task.Title() != args.title {
		t.Errorf("Task.Title() = %q, ожидалось %q", task.Title().String(), args.title.String())
	}
	if task.Description() != args.description {
		t.Errorf("Task.Description() = %q, ожидалось %q", task.Description().String(), args.description.String())
	}
	if task.Status() != todo.StatusPending {
		t.Errorf("Task.Status() = %s, ожидалось pending", task.Status())
	}
	if task.Priority() != args.priority {
		t.Errorf("Task.Priority() = %s, ожидалось %s", task.Priority(), args.priority)
	}

	due, ok := task.DueDate()
	if !ok {
		t.Error("Task.DueDate() не вернул срок, хотя он был задан при создании")
	} else if due != *args.dueDate {
		t.Errorf("Task.DueDate() = %s, ожидалось %s", due.Time(), args.dueDate.Time())
	}

	if !task.CreatedAt().Equal(testNow) {
		t.Errorf("Task.CreatedAt() = %s, ожидалось %s", task.CreatedAt(), testNow)
	}
	if !task.UpdatedAt().Equal(testNow) {
		t.Errorf("Task.UpdatedAt() = %s, ожидалось %s", task.UpdatedAt(), testNow)
	}
	if _, completed := task.CompletedAt(); completed {
		t.Error("Task.CompletedAt() вернул момент выполнения у только что созданной задачи")
	}
	if task.Version() != 1 {
		t.Errorf("Task.Version() = %d, ожидалось 1 у только что созданной задачи", task.Version())
	}
}

func TestNewTaskWithoutDueDate(t *testing.T) {
	t.Parallel()

	args := validTaskArgs(t)
	args.dueDate = nil

	task, err := args.newTask(testNow)
	if err != nil {
		t.Fatalf("NewTask(...) без срока вернул ошибку: %v", err)
	}

	if _, ok := task.DueDate(); ok {
		t.Error("Task.DueDate() вернул срок у задачи, созданной без него")
	}
	if task.IsOverdue(testEvenLater) {
		t.Error("Task.IsOverdue(...) = true у задачи без срока")
	}
}

func TestNewTaskValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spoil   func(*taskArgs)
		wantErr error
	}{
		{
			name:    "неинициализированный идентификатор задачи",
			spoil:   func(a *taskArgs) { a.id = todo.TaskID{} },
			wantErr: todo.ErrInvalidTaskID,
		},
		{
			name:    "неинициализированный идентификатор владельца",
			spoil:   func(a *taskArgs) { a.ownerID = todo.OwnerID{} },
			wantErr: todo.ErrInvalidOwnerID,
		},
		{
			name:    "неинициализированный заголовок",
			spoil:   func(a *taskArgs) { a.title = todo.Title{} },
			wantErr: todo.ErrEmptyTitle,
		},
		{
			name:    "приоритет вне перечисления",
			spoil:   func(a *taskArgs) { a.priority = todo.Priority(200) },
			wantErr: todo.ErrUnknownPriority,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := validTaskArgs(t)
			tt.spoil(&args)

			task, err := args.newTask(testNow)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewTask(...) вернул ошибку %v, ожидалась %v", err, tt.wantErr)
			}
			if task != nil {
				t.Error("NewTask(...) при ошибке вернул непустую задачу")
			}
		})
	}
}

func TestNewTaskRejectsStaleDueDate(t *testing.T) {
	t.Parallel()

	// Срок был корректен в момент своего создания, но к моменту создания
	// задачи уже прошёл. Reschedule такое отвергает — конструктор обязан
	// вести себя так же, иначе одно правило соблюдается через раз.
	args := validTaskArgs(t)
	args.dueDate = mustDueDate(t, testLater)

	task, err := args.newTask(testMuchLater)
	if !errors.Is(err, todo.ErrDueDateInPast) {
		t.Fatalf("NewTask(...) вернул ошибку %v, ожидалась ErrDueDateInPast", err)
	}
	if task != nil {
		t.Error("NewTask(...) при ошибке вернул непустую задачу")
	}
}

func TestTaskLifecycle(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)

	if err := task.Start(testLater); err != nil {
		t.Fatalf("Task.Start(...) вернул ошибку: %v", err)
	}
	if task.Status() != todo.StatusInProgress {
		t.Errorf("после Start статус = %s, ожидалось in_progress", task.Status())
	}
	if !task.UpdatedAt().Equal(testLater) {
		t.Errorf("после Start UpdatedAt = %s, ожидалось %s", task.UpdatedAt(), testLater)
	}
	if task.Version() != 2 {
		t.Errorf("после Start версия = %d, ожидалось 2", task.Version())
	}

	if err := task.Complete(testMuchLater); err != nil {
		t.Fatalf("Task.Complete(...) вернул ошибку: %v", err)
	}
	if task.Status() != todo.StatusCompleted {
		t.Errorf("после Complete статус = %s, ожидалось completed", task.Status())
	}

	completedAt, ok := task.CompletedAt()
	if !ok {
		t.Fatal("после Complete момент выполнения не проставлен")
	}
	if !completedAt.Equal(testMuchLater) {
		t.Errorf("после Complete CompletedAt = %s, ожидалось %s", completedAt, testMuchLater)
	}
	if task.Version() != 3 {
		t.Errorf("после Complete версия = %d, ожидалось 3", task.Version())
	}
}

func TestTaskCompleteDirectlyFromPending(t *testing.T) {
	t.Parallel()

	// Не всякая задача проходит через «в работе»: мелкие дела закрывают сразу.
	task := newTestTask(t)

	if err := task.Complete(testLater); err != nil {
		t.Fatalf("Task.Complete(...) из pending вернул ошибку: %v", err)
	}
	if task.Status() != todo.StatusCompleted {
		t.Errorf("статус = %s, ожидалось completed", task.Status())
	}
}

func TestTaskTransitionsFromTerminalStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		close   func(*testing.T, *todo.Task)
		action  func(*todo.Task) error
		wantErr error
	}{
		{
			name:    "повторное выполнение выполненной",
			close:   completeTask,
			action:  func(task *todo.Task) error { return task.Complete(testEvenLater) },
			wantErr: todo.ErrTaskAlreadyCompleted,
		},
		{
			name:    "взятие в работу выполненной",
			close:   completeTask,
			action:  func(task *todo.Task) error { return task.Start(testEvenLater) },
			wantErr: todo.ErrTaskAlreadyCompleted,
		},
		{
			name:    "отмена выполненной",
			close:   completeTask,
			action:  func(task *todo.Task) error { return task.Cancel(testEvenLater) },
			wantErr: todo.ErrTaskAlreadyCompleted,
		},
		{
			name:    "повторная отмена отменённой",
			close:   cancelTask,
			action:  func(task *todo.Task) error { return task.Cancel(testEvenLater) },
			wantErr: todo.ErrTaskCancelled,
		},
		{
			name:    "выполнение отменённой",
			close:   cancelTask,
			action:  func(task *todo.Task) error { return task.Complete(testEvenLater) },
			wantErr: todo.ErrTaskCancelled,
		},
		{
			name:    "взятие в работу отменённой",
			close:   cancelTask,
			action:  func(task *todo.Task) error { return task.Start(testEvenLater) },
			wantErr: todo.ErrTaskCancelled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			task := newTestTask(t)
			tt.close(t, task)

			wantStatus := task.Status()
			wantVersion := task.Version()
			wantUpdatedAt := task.UpdatedAt()

			if err := tt.action(task); !errors.Is(err, tt.wantErr) {
				t.Fatalf("операция вернула ошибку %v, ожидалась %v", err, tt.wantErr)
			}

			assertUnchanged(t, task, wantStatus, wantVersion, wantUpdatedAt)
		})
	}
}

func TestTaskCancelFromInProgress(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)
	if err := task.Start(testLater); err != nil {
		t.Fatalf("Task.Start(...) вернул ошибку: %v", err)
	}

	if err := task.Cancel(testMuchLater); err != nil {
		t.Fatalf("Task.Cancel(...) из in_progress вернул ошибку: %v", err)
	}
	if task.Status() != todo.StatusCancelled {
		t.Errorf("статус = %s, ожидалось cancelled", task.Status())
	}
	if _, ok := task.CompletedAt(); ok {
		t.Error("у отменённой задачи проставлен момент выполнения")
	}
}

func TestTaskRename(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)
	newTitle := mustTitle(t, "Купить кефир")

	if err := task.Rename(newTitle, testLater); err != nil {
		t.Fatalf("Task.Rename(...) вернул ошибку: %v", err)
	}

	if task.Title() != newTitle {
		t.Errorf("Task.Title() = %q, ожидалось %q", task.Title().String(), newTitle.String())
	}
	if !task.UpdatedAt().Equal(testLater) {
		t.Errorf("Task.UpdatedAt() = %s, ожидалось %s", task.UpdatedAt(), testLater)
	}
	if !task.CreatedAt().Equal(testNow) {
		t.Errorf("Task.CreatedAt() = %s, момент создания менять нельзя", task.CreatedAt())
	}
	if task.Version() != 2 {
		t.Errorf("Task.Version() = %d, ожидалось 2", task.Version())
	}
}

// mutation — именованная операция изменения задачи.
//
// Список мутаций один на все тесты: пока каждый тест перечисляет операции
// заново, следующая операция снова окажется покрыта наполовину — ровно так
// в модели и разошлись updatedAt и проверка терминальных статусов.
type mutation struct {
	name  string
	apply func(t *testing.T, task *todo.Task, now time.Time) error
}

// fieldMutations — операции, меняющие поля задачи, но не её статус.
func fieldMutations() []mutation {
	return []mutation{
		{
			name: "переименование",
			apply: func(t *testing.T, task *todo.Task, now time.Time) error {
				return task.Rename(mustTitle(t, "Купить кефир"), now)
			},
		},
		{
			name: "смена описания",
			apply: func(t *testing.T, task *todo.Task, now time.Time) error {
				return task.Describe(mustDescription(t, "Лучше взять два по литру"), now)
			},
		},
		{
			name: "смена приоритета",
			apply: func(t *testing.T, task *todo.Task, now time.Time) error {
				return task.ChangePriority(todo.PriorityCritical, now)
			},
		},
		{
			name: "перенос срока",
			apply: func(t *testing.T, task *todo.Task, now time.Time) error {
				due, err := todo.NewDueDate(now.Add(24*time.Hour), now)
				if err != nil {
					t.Fatalf("NewDueDate(...) вернул ошибку: %v", err)
				}
				return task.Reschedule(&due, now)
			},
		},
	}
}

// statusMutations — операции, двигающие задачу по конечному автомату.
// Все они допустимы из статуса pending.
func statusMutations() []mutation {
	return []mutation{
		{
			name:  "взятие в работу",
			apply: func(_ *testing.T, task *todo.Task, now time.Time) error { return task.Start(now) },
		},
		{
			name:  "выполнение",
			apply: func(_ *testing.T, task *todo.Task, now time.Time) error { return task.Complete(now) },
		},
		{
			name:  "отмена",
			apply: func(_ *testing.T, task *todo.Task, now time.Time) error { return task.Cancel(now) },
		},
	}
}

// sameValueMutations — операции, которым скармливают то значение, что у задачи
// уже стоит.
//
// Ровно это делает клиент, присылающий форму целиком: какие поля пользователь
// тронул, он не знает, поэтому шлёт все. Добавляя мутатор в fieldMutations,
// добавить его и сюда — иначе повтор на нём останется незамеченным.
func sameValueMutations() []mutation {
	return []mutation{
		{
			name: "тот же заголовок",
			apply: func(_ *testing.T, task *todo.Task, now time.Time) error {
				return task.Rename(task.Title(), now)
			},
		},
		{
			name: "то же описание",
			apply: func(_ *testing.T, task *todo.Task, now time.Time) error {
				return task.Describe(task.Description(), now)
			},
		},
		{
			name: "тот же приоритет",
			apply: func(_ *testing.T, task *todo.Task, now time.Time) error {
				return task.ChangePriority(task.Priority(), now)
			},
		},
		{
			name: "тот же срок",
			apply: func(_ *testing.T, task *todo.Task, now time.Time) error {
				due, ok := task.DueDate()
				if !ok {
					return task.Reschedule(nil, now)
				}
				return task.Reschedule(&due, now)
			},
		},
	}
}

func TestTaskMutationsWithSameValueChangeNothing(t *testing.T) {
	t.Parallel()

	// Мутация, ничего не меняющая, — не мутация. Двигать версию и порождать
	// событие на ней нельзя: подписчики получат «изменилось» на неизменившейся
	// задаче, а параллельный редактор, державший прежнюю версию, поймает
	// конфликт на пустом месте.
	for _, mut := range sameValueMutations() {
		t.Run(mut.name, func(t *testing.T) {
			t.Parallel()

			task := newTestTaskWithDueDate(t, mustDueDate(t, testNow.Add(24*time.Hour)))
			// События создания забираем заранее: смотреть надо только на то,
			// что породил повтор.
			task.PullEvents()

			wantVersion := task.Version()
			wantUpdatedAt := task.UpdatedAt()

			if err := mut.apply(t, task, testNow.Add(time.Hour)); err != nil {
				t.Fatalf("повтор вернул ошибку: %v", err)
			}

			if got := task.Version(); got != wantVersion {
				t.Errorf("версия = %d, ожидалась %d", got, wantVersion)
			}
			if got := task.UpdatedAt(); !got.Equal(wantUpdatedAt) {
				t.Errorf("updatedAt = %s, ожидалось %s", got, wantUpdatedAt)
			}
			if events := task.PullEvents(); len(events) != 0 {
				t.Errorf("события = %v, ожидалось пусто", eventNames(events))
			}
		})
	}

	t.Run("снятие отсутствующего срока", func(t *testing.T) {
		t.Parallel()

		// Отсутствие срока — тоже значение, и снять его дважды нельзя.
		task := newTestTask(t)
		task.PullEvents()

		wantVersion := task.Version()
		wantUpdatedAt := task.UpdatedAt()

		if err := task.Reschedule(nil, testNow.Add(time.Hour)); err != nil {
			t.Fatalf("Reschedule(nil) вернул ошибку: %v", err)
		}

		if got := task.Version(); got != wantVersion {
			t.Errorf("версия = %d, ожидалась %d", got, wantVersion)
		}
		if got := task.UpdatedAt(); !got.Equal(wantUpdatedAt) {
			t.Errorf("updatedAt = %s, ожидалось %s", got, wantUpdatedAt)
		}
		if events := task.PullEvents(); len(events) != 0 {
			t.Errorf("события = %v, ожидалось пусто", eventNames(events))
		}
	})
}

func TestRescheduleToOwnOverdueDate(t *testing.T) {
	t.Parallel()

	// Задача с просроченным сроком законно живёт в хранилище, а TaskView
	// отдаёт срок наружу — значит клиент, вернувший прочитанную форму целиком,
	// пришлёт его обратно. Это повтор, а не назначение, и требовать от него
	// будущего нельзя: иначе просроченную задачу не переименовать.
	task := newTestTaskWithDueDate(t, mustDueDate(t, testTomorrowAt))
	task.PullEvents()

	wantVersion := task.Version()
	wantUpdatedAt := task.UpdatedAt()

	// «Сейчас» уже позже срока.
	overdueNow := testTomorrowAt.Add(time.Hour)

	same, ok := task.DueDate()
	if !ok {
		t.Fatal("у задачи нет срока")
	}

	t.Run("свой срок принимается и ничего не меняет", func(t *testing.T) {
		if err := task.Reschedule(&same, overdueNow); err != nil {
			t.Fatalf("повтор просроченного срока вернул ошибку: %v", err)
		}
		if got := task.Version(); got != wantVersion {
			t.Errorf("версия = %d, ожидалась %d", got, wantVersion)
		}
		if got := task.UpdatedAt(); !got.Equal(wantUpdatedAt) {
			t.Errorf("updatedAt = %s, ожидалось %s", got, wantUpdatedAt)
		}
		if events := task.PullEvents(); len(events) != 0 {
			t.Errorf("события = %v, ожидалось пусто", eventNames(events))
		}
	})

	t.Run("чужой просроченный срок по-прежнему отвергается", func(t *testing.T) {
		// Послабление касается только повтора: назначить другой срок в прошлом
		// нельзя, иначе проверка перестала бы существовать.
		other := mustDueDate(t, testNow.Add(2*time.Hour))

		if err := task.Reschedule(other, overdueNow); !errors.Is(err, todo.ErrDueDateInPast) {
			t.Fatalf("ожидалась ErrDueDateInPast, получено: %v", err)
		}
	})

	t.Run("HasDueDateAt узнаёт свой момент", func(t *testing.T) {
		if !task.HasDueDateAt(testTomorrowAt) {
			t.Error("задача не узнала собственный срок")
		}
		if task.HasDueDateAt(testTomorrowAt.Add(time.Second)) {
			t.Error("задача признала своим чужой момент")
		}
		if newTestTask(t).HasDueDateAt(testTomorrowAt) {
			t.Error("задача без срока признала момент своим")
		}
	})
}

func TestTaskMutationsRejectedOnTerminalStatus(t *testing.T) {
	t.Parallel()

	// Терминальный статус означает, что задача дожила до конца жизненного
	// цикла: менять её поля нельзя независимо от того, каким именно образом
	// она туда попала.
	terminals := []struct {
		name    string
		close   func(*testing.T, *todo.Task)
		wantErr error
	}{
		{name: "выполненная", close: completeTask, wantErr: todo.ErrTaskAlreadyCompleted},
		{name: "отменённая", close: cancelTask, wantErr: todo.ErrTaskCancelled},
	}

	for _, terminal := range terminals {
		for _, mut := range fieldMutations() {
			t.Run(terminal.name+"/"+mut.name, func(t *testing.T) {
				t.Parallel()

				task := newTestTask(t)
				terminal.close(t, task)

				wantTitle := task.Title()
				wantDescription := task.Description()
				wantPriority := task.Priority()
				wantStatus := task.Status()
				wantVersion := task.Version()
				wantUpdatedAt := task.UpdatedAt()

				if err := mut.apply(t, task, testEvenLater); !errors.Is(err, terminal.wantErr) {
					t.Fatalf("операция вернула ошибку %v, ожидалась %v", err, terminal.wantErr)
				}

				if task.Title() != wantTitle {
					t.Errorf("после отклонённой операции заголовок = %q, ожидалось %q",
						task.Title().String(), wantTitle.String())
				}
				if task.Description() != wantDescription {
					t.Errorf("после отклонённой операции описание = %q, ожидалось %q",
						task.Description().String(), wantDescription.String())
				}
				if task.Priority() != wantPriority {
					t.Errorf("после отклонённой операции приоритет = %s, ожидалось %s",
						task.Priority(), wantPriority)
				}
				if _, ok := task.DueDate(); ok {
					t.Error("после отклонённой операции у задачи появился срок")
				}
				assertUnchanged(t, task, wantStatus, wantVersion, wantUpdatedAt)
			})
		}
	}
}

func TestTaskMutationsAdvanceUpdatedAtAndVersion(t *testing.T) {
	t.Parallel()

	// Всякая успешная мутация — это новое состояние агрегата: она обязана
	// сдвинуть updatedAt и версию. По updatedAt хранилище строит ленту,
	// по версии работает оптимистичная блокировка.
	for _, mut := range append(fieldMutations(), statusMutations()...) {
		t.Run(mut.name, func(t *testing.T) {
			t.Parallel()

			task := newTestTask(t)

			if err := mut.apply(t, task, testLater); err != nil {
				t.Fatalf("операция вернула ошибку: %v", err)
			}

			if !task.UpdatedAt().Equal(testLater) {
				t.Errorf("Task.UpdatedAt() = %s, ожидалось %s", task.UpdatedAt(), testLater)
			}
			if task.Version() != 2 {
				t.Errorf("Task.Version() = %d, ожидалось 2", task.Version())
			}
			if !task.CreatedAt().Equal(testNow) {
				t.Errorf("Task.CreatedAt() = %s, момент создания менять нельзя", task.CreatedAt())
			}
		})
	}
}

func TestTaskStartTwice(t *testing.T) {
	t.Parallel()

	// Единственный источник правды о переходах — Status.CanTransitionTo,
	// и переход в собственный статус он запрещает.
	task := newTestTask(t)
	if err := task.Start(testLater); err != nil {
		t.Fatalf("Task.Start(...) вернул ошибку: %v", err)
	}

	wantVersion := task.Version()
	wantUpdatedAt := task.UpdatedAt()

	if err := task.Start(testMuchLater); !errors.Is(err, todo.ErrInvalidStatusTransition) {
		t.Fatalf("повторный Task.Start(...) вернул ошибку %v, ожидалась ErrInvalidStatusTransition", err)
	}
	assertUnchanged(t, task, todo.StatusInProgress, wantVersion, wantUpdatedAt)
}

func TestTaskRenameToZeroTitle(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)
	wantTitle := task.Title()

	if err := task.Rename(todo.Title{}, testLater); !errors.Is(err, todo.ErrEmptyTitle) {
		t.Fatalf("Task.Rename(Title{}, ...) вернул ошибку %v, ожидалась ErrEmptyTitle", err)
	}
	if task.Title() != wantTitle {
		t.Errorf("заголовок изменился на %q, ожидалось %q", task.Title().String(), wantTitle.String())
	}
	if task.Version() != 1 {
		t.Errorf("версия = %d, отклонённая операция не должна её двигать", task.Version())
	}
}

func TestTaskDescribe(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)
	newDescription := mustDescription(t, "Лучше взять два по литру")

	if err := task.Describe(newDescription, testLater); err != nil {
		t.Fatalf("Task.Describe(...) вернул ошибку: %v", err)
	}
	if task.Description() != newDescription {
		t.Errorf("Task.Description() = %q, ожидалось %q",
			task.Description().String(), newDescription.String())
	}
	if task.Version() != 2 {
		t.Errorf("Task.Version() = %d, ожидалось 2", task.Version())
	}
}

func TestTaskChangePriority(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)

	if err := task.ChangePriority(todo.PriorityCritical, testLater); err != nil {
		t.Fatalf("Task.ChangePriority(...) вернул ошибку: %v", err)
	}
	if task.Priority() != todo.PriorityCritical {
		t.Errorf("Task.Priority() = %s, ожидалось critical", task.Priority())
	}

	wantVersion := task.Version()
	if err := task.ChangePriority(todo.Priority(200), testMuchLater); !errors.Is(err, todo.ErrUnknownPriority) {
		t.Fatalf("Task.ChangePriority(200, ...) вернул ошибку %v, ожидалась ErrUnknownPriority", err)
	}
	if task.Priority() != todo.PriorityCritical {
		t.Errorf("после отклонённой смены приоритет = %s, ожидалось critical", task.Priority())
	}
	if task.Version() != wantVersion {
		t.Errorf("версия = %d, ожидалось %d: отклонённая операция её не двигает", task.Version(), wantVersion)
	}
}

func TestTaskReschedule(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)
	due := mustDueDate(t, testTomorrowAt)

	if err := task.Reschedule(due, testLater); err != nil {
		t.Fatalf("Task.Reschedule(...) вернул ошибку: %v", err)
	}

	got, ok := task.DueDate()
	if !ok {
		t.Fatal("Task.DueDate() не вернул срок после Reschedule")
	}
	if got != *due {
		t.Errorf("Task.DueDate() = %s, ожидалось %s", got.Time(), due.Time())
	}
}

func TestTaskRescheduleClearsDueDate(t *testing.T) {
	t.Parallel()

	task := newTestTaskWithDueDate(t, mustDueDate(t, testTomorrowAt))

	if err := task.Reschedule(nil, testLater); err != nil {
		t.Fatalf("Task.Reschedule(nil, ...) вернул ошибку: %v", err)
	}
	if _, ok := task.DueDate(); ok {
		t.Error("Task.DueDate() вернул срок после его снятия")
	}
	if task.Version() != 2 {
		t.Errorf("Task.Version() = %d, ожидалось 2", task.Version())
	}
}

func TestTaskRescheduleIntoPast(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)

	// Срок был корректен в момент testNow, но к testMuchLater уже протух:
	// значимый объект проверяли один раз, а агрегат обязан проверить снова.
	stale := mustDueDate(t, testLater)

	if err := task.Reschedule(stale, testMuchLater); !errors.Is(err, todo.ErrDueDateInPast) {
		t.Fatalf("Task.Reschedule(...) вернул ошибку %v, ожидалась ErrDueDateInPast", err)
	}
	if _, ok := task.DueDate(); ok {
		t.Error("после отклонённого переноса у задачи появился срок")
	}
	if task.Version() != 1 {
		t.Errorf("версия = %d, отклонённая операция не должна её двигать", task.Version())
	}
}

func TestTaskIsOverdue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dueDate func(*testing.T) *todo.DueDate
		close   func(*testing.T, *todo.Task)
		at      time.Time
		want    bool
	}{
		{
			name:    "срок не задан",
			dueDate: func(*testing.T) *todo.DueDate { return nil },
			at:      testEvenLater,
			want:    false,
		},
		{
			name:    "срок ещё не наступил",
			dueDate: func(t *testing.T) *todo.DueDate { return mustDueDate(t, testTomorrowAt) },
			at:      testLater,
			want:    false,
		},
		{
			name:    "момент ровно в срок просрочкой не считается",
			dueDate: func(t *testing.T) *todo.DueDate { return mustDueDate(t, testTomorrowAt) },
			at:      testTomorrowAt,
			want:    false,
		},
		{
			name:    "срок истёк",
			dueDate: func(t *testing.T) *todo.DueDate { return mustDueDate(t, testLater) },
			at:      testMuchLater,
			want:    true,
		},
		{
			name:    "выполненная задача не бывает просроченной",
			dueDate: func(t *testing.T) *todo.DueDate { return mustDueDate(t, testLater) },
			close:   completeTask,
			at:      testEvenLater,
			want:    false,
		},
		{
			name:    "отменённая задача не бывает просроченной",
			dueDate: func(t *testing.T) *todo.DueDate { return mustDueDate(t, testLater) },
			close:   cancelTask,
			at:      testEvenLater,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			task := newTestTaskWithDueDate(t, tt.dueDate(t))
			if tt.close != nil {
				tt.close(t, task)
			}

			if got := task.IsOverdue(tt.at); got != tt.want {
				t.Errorf("Task.IsOverdue(%s) = %v, ожидалось %v", tt.at, got, tt.want)
			}
		})
	}
}

func TestTaskSnapshotRoundTrip(t *testing.T) {
	t.Parallel()

	// Круг через хранилище обязан быть безубыточным для любого состояния,
	// в котором задача может там оказаться, — а не только для того,
	// про которое вспомнил автор теста.
	tests := []struct {
		name    string
		prepare func(*testing.T, *todo.Task)
	}{
		{name: "ожидает выполнения", prepare: func(*testing.T, *todo.Task) {}},
		{name: "в работе", prepare: startTask},
		{name: "выполненная", prepare: completeTask},
		{name: "отменённая", prepare: cancelTask},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			original := newTestTaskWithDueDate(t, mustDueDate(t, testTomorrowAt))
			tt.prepare(t, original)
			original.PullEvents()

			restored, err := todo.ReconstituteTask(original.Snapshot())
			if err != nil {
				t.Fatalf("ReconstituteTask(...) вернул ошибку: %v", err)
			}

			assertSameState(t, original, restored)

			// Восстановление из хранилища — не событие доменной жизни.
			if events := restored.PullEvents(); len(events) != 0 {
				t.Errorf("восстановленная задача несёт события %v, ожидалось ни одного", eventNames(events))
			}
		})
	}
}

func TestReconstituteTaskKeepsStaleDueDate(t *testing.T) {
	t.Parallel()

	// Инварианты создания к восстановлению не применяются: задача с давно
	// просроченным сроком законно лежит в хранилище и обязана оттуда подняться.
	task := newTestTaskWithDueDate(t, mustDueDate(t, testLater))
	snapshot := task.Snapshot()
	snapshot.UpdatedAt = testTomorrowAt

	restored, err := todo.ReconstituteTask(snapshot)
	if err != nil {
		t.Fatalf("ReconstituteTask(...) вернул ошибку: %v", err)
	}
	if _, ok := restored.DueDate(); !ok {
		t.Error("восстановленная задача потеряла просроченный срок")
	}
	if !restored.IsOverdue(testTomorrowAt) {
		t.Error("Task.IsOverdue(...) = false, ожидалось true для просроченной задачи")
	}
}

func TestReconstituteTaskValidation(t *testing.T) {
	t.Parallel()

	valid := newTestTaskWithDueDate(t, mustDueDate(t, testTomorrowAt)).Snapshot()

	tests := []struct {
		name    string
		spoil   func(*todo.TaskSnapshot)
		wantErr error
	}{
		{
			name:    "пустой снимок",
			spoil:   func(s *todo.TaskSnapshot) { *s = todo.TaskSnapshot{} },
			wantErr: todo.ErrInvalidTaskID,
		},
		{
			name:    "неинициализированный владелец",
			spoil:   func(s *todo.TaskSnapshot) { s.OwnerID = todo.OwnerID{} },
			wantErr: todo.ErrInvalidOwnerID,
		},
		{
			name:    "неинициализированный заголовок",
			spoil:   func(s *todo.TaskSnapshot) { s.Title = todo.Title{} },
			wantErr: todo.ErrEmptyTitle,
		},
		{
			name:    "статус вне перечисления",
			spoil:   func(s *todo.TaskSnapshot) { s.Status = todo.Status(200) },
			wantErr: todo.ErrUnknownStatus,
		},
		{
			name:    "приоритет вне перечисления",
			spoil:   func(s *todo.TaskSnapshot) { s.Priority = todo.Priority(200) },
			wantErr: todo.ErrUnknownPriority,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			snapshot := valid
			tt.spoil(&snapshot)

			task, err := todo.ReconstituteTask(snapshot)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReconstituteTask(...) вернул ошибку %v, ожидалась %v", err, tt.wantErr)
			}
			if task != nil {
				t.Error("ReconstituteTask(...) при ошибке вернул непустую задачу")
			}
		})
	}
}

func TestTaskDoesNotShareMutableState(t *testing.T) {
	t.Parallel()

	// Присваивание структуры в Go копирует указатель, а не то, на что он
	// смотрит. Поэтому всё, что пересекает границу агрегата в виде *DueDate
	// или *time.Time, обязано быть копией: иначе состояние задачи можно
	// изменить снаружи, минуя методы, версию и события.
	t.Run("срок, переданный в конструктор", func(t *testing.T) {
		t.Parallel()

		due := mustDueDate(t, testTomorrowAt)
		task := newTestTaskWithDueDate(t, due)

		*due = *mustDueDate(t, testTomorrowAt.Add(time.Hour))

		assertDueDate(t, task, testTomorrowAt)
	})

	t.Run("срок, переданный в Reschedule", func(t *testing.T) {
		t.Parallel()

		task := newTestTask(t)
		due := mustDueDate(t, testTomorrowAt)
		if err := task.Reschedule(due, testLater); err != nil {
			t.Fatalf("Task.Reschedule(...) вернул ошибку: %v", err)
		}

		*due = *mustDueDate(t, testTomorrowAt.Add(time.Hour))

		assertDueDate(t, task, testTomorrowAt)
	})

	t.Run("срок в снимке", func(t *testing.T) {
		t.Parallel()

		task := newTestTaskWithDueDate(t, mustDueDate(t, testTomorrowAt))
		snapshot := task.Snapshot()

		*snapshot.DueDate = *mustDueDate(t, testTomorrowAt.Add(time.Hour))

		assertDueDate(t, task, testTomorrowAt)
	})

	t.Run("момент выполнения в снимке", func(t *testing.T) {
		t.Parallel()

		task := newTestTask(t)
		completeTask(t, task)
		snapshot := task.Snapshot()

		*snapshot.CompletedAt = testEvenLater

		got, ok := task.CompletedAt()
		if !ok {
			t.Fatal("задача потеряла момент выполнения")
		}
		if !got.Equal(testLater) {
			t.Errorf("Task.CompletedAt() = %s, ожидалось %s", got, testLater)
		}
	})

	t.Run("срок, попавший в снимок при восстановлении", func(t *testing.T) {
		t.Parallel()

		snapshot := newTestTaskWithDueDate(t, mustDueDate(t, testTomorrowAt)).Snapshot()

		restored, err := todo.ReconstituteTask(snapshot)
		if err != nil {
			t.Fatalf("ReconstituteTask(...) вернул ошибку: %v", err)
		}

		*snapshot.DueDate = *mustDueDate(t, testTomorrowAt.Add(time.Hour))

		assertDueDate(t, restored, testTomorrowAt)
	})

	t.Run("срок в доменном событии", func(t *testing.T) {
		t.Parallel()

		// Событие — неизменяемый факт о прошлом: изменить его задним числом
		// не должен никто, включая того, кто его получил.
		task := newTestTaskWithDueDate(t, mustDueDate(t, testTomorrowAt))

		events := task.PullEvents()
		if len(events) != 1 {
			t.Fatalf("NewTask породил события %v, ожидалось ровно одно", eventNames(events))
		}
		created, ok := events[0].(todo.TaskCreated)
		if !ok {
			t.Fatalf("тип события = %T, ожидалось todo.TaskCreated", events[0])
		}

		*created.DueDate = *mustDueDate(t, testTomorrowAt.Add(time.Hour))

		assertDueDate(t, task, testTomorrowAt)
	})
}

// assertDueDate проверяет, что срок задачи совпадает с ожидаемым моментом.
func assertDueDate(t *testing.T, task *todo.Task, want time.Time) {
	t.Helper()

	got, ok := task.DueDate()
	if !ok {
		t.Fatal("задача потеряла срок")
	}
	if !got.Time().Equal(want) {
		t.Errorf("Task.DueDate() = %s, ожидалось %s", got.Time(), want)
	}
}

// startTask берёт задачу в работу в момент testLater.
func startTask(t *testing.T, task *todo.Task) {
	t.Helper()

	if err := task.Start(testLater); err != nil {
		t.Fatalf("Task.Start(...) вернул ошибку: %v", err)
	}
}

// completeTask выполняет задачу в момент testLater.
func completeTask(t *testing.T, task *todo.Task) {
	t.Helper()

	if err := task.Complete(testLater); err != nil {
		t.Fatalf("Task.Complete(...) вернул ошибку: %v", err)
	}
}

// cancelTask отменяет задачу в момент testLater.
func cancelTask(t *testing.T, task *todo.Task) {
	t.Helper()

	if err := task.Cancel(testLater); err != nil {
		t.Fatalf("Task.Cancel(...) вернул ошибку: %v", err)
	}
}

// assertUnchanged проверяет, что отклонённая операция не оставила следов.
func assertUnchanged(t *testing.T, task *todo.Task, status todo.Status, version int, updatedAt time.Time) {
	t.Helper()

	if task.Status() != status {
		t.Errorf("статус = %s, ожидалось %s: отклонённая операция его не меняет", task.Status(), status)
	}
	if task.Version() != version {
		t.Errorf("версия = %d, ожидалось %d: отклонённая операция её не двигает", task.Version(), version)
	}
	if !task.UpdatedAt().Equal(updatedAt) {
		t.Errorf("UpdatedAt = %s, ожидалось %s: отклонённая операция его не двигает", task.UpdatedAt(), updatedAt)
	}
}

// assertSameState сравнивает наблюдаемое состояние двух задач.
func assertSameState(t *testing.T, want, got *todo.Task) {
	t.Helper()

	if got.ID() != want.ID() {
		t.Errorf("ID = %q, ожидалось %q", got.ID().String(), want.ID().String())
	}
	if got.OwnerID() != want.OwnerID() {
		t.Errorf("OwnerID = %q, ожидалось %q", got.OwnerID().String(), want.OwnerID().String())
	}
	if got.Title() != want.Title() {
		t.Errorf("заголовок = %q, ожидалось %q", got.Title().String(), want.Title().String())
	}
	if got.Description() != want.Description() {
		t.Errorf("описание = %q, ожидалось %q", got.Description().String(), want.Description().String())
	}
	if got.Status() != want.Status() {
		t.Errorf("статус = %s, ожидалось %s", got.Status(), want.Status())
	}
	if got.Priority() != want.Priority() {
		t.Errorf("приоритет = %s, ожидалось %s", got.Priority(), want.Priority())
	}
	if got.Version() != want.Version() {
		t.Errorf("версия = %d, ожидалось %d", got.Version(), want.Version())
	}
	if !got.CreatedAt().Equal(want.CreatedAt()) {
		t.Errorf("CreatedAt = %s, ожидалось %s", got.CreatedAt(), want.CreatedAt())
	}
	if !got.UpdatedAt().Equal(want.UpdatedAt()) {
		t.Errorf("UpdatedAt = %s, ожидалось %s", got.UpdatedAt(), want.UpdatedAt())
	}

	gotDue, gotOK := got.DueDate()
	wantDue, wantOK := want.DueDate()
	if gotOK != wantOK || gotDue != wantDue {
		t.Errorf("срок = (%s, %v), ожидалось (%s, %v)", gotDue.Time(), gotOK, wantDue.Time(), wantOK)
	}

	gotCompletedAt, gotOK := got.CompletedAt()
	wantCompletedAt, wantOK := want.CompletedAt()
	if gotOK != wantOK || !gotCompletedAt.Equal(wantCompletedAt) {
		t.Errorf("момент выполнения = (%s, %v), ожидалось (%s, %v)",
			gotCompletedAt, gotOK, wantCompletedAt, wantOK)
	}
}
