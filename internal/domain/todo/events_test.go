package todo_test

import (
	"slices"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

func TestNewTaskEmitsTaskCreated(t *testing.T) {
	t.Parallel()

	args := validTaskArgs(t)

	task, err := args.newTask(testNow)
	if err != nil {
		t.Fatalf("NewTask(...) вернул ошибку: %v", err)
	}

	events := task.PullEvents()
	if len(events) != 1 {
		t.Fatalf("NewTask(...) породил события %v, ожидалось ровно одно", todotest.EventNames(events))
	}

	created, ok := events[0].(todo.TaskCreated)
	if !ok {
		t.Fatalf("тип события = %T, ожидалось todo.TaskCreated", events[0])
	}

	if created.EventName() != todo.EventTaskCreated {
		t.Errorf("EventName() = %q, ожидалось %q", created.EventName(), todo.EventTaskCreated)
	}
	if created.AggregateID() != args.id {
		t.Errorf("AggregateID() = %q, ожидалось %q", created.AggregateID().String(), args.id.String())
	}
	if !created.OccurredAt().Equal(testNow) {
		t.Errorf("OccurredAt() = %s, ожидалось %s", created.OccurredAt(), testNow)
	}
	if created.OwnerID != args.ownerID {
		t.Errorf("OwnerID = %q, ожидалось %q", created.OwnerID.String(), args.ownerID.String())
	}
	if created.Title != args.title {
		t.Errorf("Title = %q, ожидалось %q", created.Title.String(), args.title.String())
	}
	if created.Description != args.description {
		t.Errorf("Description = %q, ожидалось %q", created.Description.String(), args.description.String())
	}
	if created.Priority != args.priority {
		t.Errorf("Priority = %s, ожидалось %s", created.Priority, args.priority)
	}
	if created.DueDate == nil || *created.DueDate != *args.dueDate {
		t.Errorf("DueDate = %v, ожидалось %s", created.DueDate, args.dueDate.Time())
	}
}

func TestPullEventsDrainsBuffer(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)

	if events := task.PullEvents(); len(events) != 1 {
		t.Fatalf("первый PullEvents() вернул %v, ожидалось одно событие", todotest.EventNames(events))
	}
	if events := task.PullEvents(); len(events) != 0 {
		t.Errorf("второй PullEvents() вернул %v, ожидался пустой результат", todotest.EventNames(events))
	}
}

func TestEventsFollowOperationOrder(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)

	if err := task.Rename(todotest.MustTitle(t, "Купить кефир"), testLater); err != nil {
		t.Fatalf("Task.Rename(...) вернул ошибку: %v", err)
	}
	if err := task.ChangePriority(todo.PriorityCritical, testLater); err != nil {
		t.Fatalf("Task.ChangePriority(...) вернул ошибку: %v", err)
	}
	if err := task.Start(testMuchLater); err != nil {
		t.Fatalf("Task.Start(...) вернул ошибку: %v", err)
	}
	if err := task.Complete(testEvenLater); err != nil {
		t.Fatalf("Task.Complete(...) вернул ошибку: %v", err)
	}

	want := []string{
		todo.EventTaskCreated,
		todo.EventTaskRenamed,
		todo.EventTaskPriorityChanged,
		todo.EventTaskStarted,
		todo.EventTaskCompleted,
	}

	if got := todotest.EventNames(task.PullEvents()); !slices.Equal(got, want) {
		t.Errorf("порядок событий = %v, ожидалось %v", got, want)
	}
}

func TestEventCarriesOperationTime(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)
	task.PullEvents()

	newTitle := todotest.MustTitle(t, "Купить кефир")
	if err := task.Rename(newTitle, testLater); err != nil {
		t.Fatalf("Task.Rename(...) вернул ошибку: %v", err)
	}

	events := task.PullEvents()
	if len(events) != 1 {
		t.Fatalf("Rename породил события %v, ожидалось ровно одно", todotest.EventNames(events))
	}

	renamed, ok := events[0].(todo.TaskRenamed)
	if !ok {
		t.Fatalf("тип события = %T, ожидалось todo.TaskRenamed", events[0])
	}
	if renamed.NewTitle != newTitle {
		t.Errorf("NewTitle = %q, ожидалось %q", renamed.NewTitle.String(), newTitle.String())
	}
	// Событие датируется моментом операции, а не моментом чтения буфера.
	if !renamed.OccurredAt().Equal(testLater) {
		t.Errorf("OccurredAt() = %s, ожидалось %s", renamed.OccurredAt(), testLater)
	}
	if renamed.AggregateID() != task.ID() {
		t.Errorf("AggregateID() = %q, ожидалось %q", renamed.AggregateID().String(), task.ID().String())
	}
}

func TestRejectedOperationEmitsNoEvents(t *testing.T) {
	t.Parallel()

	task := newTestTask(t)
	completeTask(t, task)
	task.PullEvents()

	if err := task.Rename(todotest.MustTitle(t, "Купить кефир"), testEvenLater); err == nil {
		t.Fatal("Task.Rename(...) на выполненной задаче не вернул ошибку")
	}
	if err := task.Start(testEvenLater); err == nil {
		t.Fatal("Task.Start(...) на выполненной задаче не вернул ошибку")
	}

	if events := task.PullEvents(); len(events) != 0 {
		t.Errorf("отклонённые операции породили события %v, ожидалось ни одного", todotest.EventNames(events))
	}
}

func TestRescheduleEmitsEventWithNewDueDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dueDate func(*testing.T) *todo.DueDate
		wantNil bool
	}{
		{
			// Срок именно другой: задача создаётся с testTomorrowAt, и перенос
			// на тот же момент изменением не является — события не будет.
			name: "назначение срока",
			dueDate: func(t *testing.T) *todo.DueDate {
				return todotest.MustDueDate(t, testTomorrowAt.Add(24*time.Hour), testNow)
			},
		},
		{
			name:    "снятие срока",
			dueDate: func(*testing.T) *todo.DueDate { return nil },
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			task := newTestTaskWithDueDate(t, todotest.MustDueDate(t, testTomorrowAt, testNow))
			task.PullEvents()

			want := tt.dueDate(t)
			if err := task.Reschedule(want, testLater); err != nil {
				t.Fatalf("Task.Reschedule(...) вернул ошибку: %v", err)
			}

			events := task.PullEvents()
			if len(events) != 1 {
				t.Fatalf("Reschedule породил события %v, ожидалось ровно одно", todotest.EventNames(events))
			}

			rescheduled, ok := events[0].(todo.TaskRescheduled)
			if !ok {
				t.Fatalf("тип события = %T, ожидалось todo.TaskRescheduled", events[0])
			}
			if rescheduled.EventName() != todo.EventTaskRescheduled {
				t.Errorf("EventName() = %q, ожидалось %q", rescheduled.EventName(), todo.EventTaskRescheduled)
			}

			switch {
			case tt.wantNil && rescheduled.NewDueDate != nil:
				t.Errorf("NewDueDate = %s, ожидался nil при снятии срока", rescheduled.NewDueDate.Time())
			case !tt.wantNil && rescheduled.NewDueDate == nil:
				t.Errorf("NewDueDate = nil, ожидалось %s", want.Time())
			case !tt.wantNil && *rescheduled.NewDueDate != *want:
				t.Errorf("NewDueDate = %s, ожидалось %s", rescheduled.NewDueDate.Time(), want.Time())
			}
		})
	}
}

func TestTerminalEventsCarryTerminalTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		close     func(*testing.T, *todo.Task)
		wantName  string
		wantEvent func(todo.DomainEvent) bool
	}{
		{
			name:     "выполнение",
			close:    completeTask,
			wantName: todo.EventTaskCompleted,
			wantEvent: func(e todo.DomainEvent) bool {
				_, ok := e.(todo.TaskCompleted)
				return ok
			},
		},
		{
			name:     "отмена",
			close:    cancelTask,
			wantName: todo.EventTaskCancelled,
			wantEvent: func(e todo.DomainEvent) bool {
				_, ok := e.(todo.TaskCancelled)
				return ok
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			task := newTestTask(t)
			task.PullEvents()
			tt.close(t, task)

			events := task.PullEvents()
			if len(events) != 1 {
				t.Fatalf("операция породила события %v, ожидалось ровно одно", todotest.EventNames(events))
			}
			if !tt.wantEvent(events[0]) {
				t.Fatalf("тип события = %T, не соответствует ожидаемому", events[0])
			}
			if got := events[0].EventName(); got != tt.wantName {
				t.Errorf("EventName() = %q, ожидалось %q", got, tt.wantName)
			}
			if got := events[0].OccurredAt(); !got.Equal(testLater) {
				t.Errorf("OccurredAt() = %s, ожидалось %s", got, testLater)
			}
		})
	}
}
