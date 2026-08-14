package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// TaskService — сценарии использования агрегата Task.
//
// Сервис ничего не решает о задачах: он разбирает ввод, поднимает агрегат,
// проверяет права на него и вызывает ровно один доменный метод. Все правила
// остаются в домене, вся оркестровка — здесь.
type TaskService struct {
	repo      Repository
	publisher EventPublisher
	clock     Clock
}

// NewTaskService собирает сервис. Все зависимости обязательны: подсунуть
// nil-публикатор вместо NopPublisher — тихо потерять события.
func NewTaskService(repo Repository, publisher EventPublisher, clock Clock) (*TaskService, error) {
	if repo == nil {
		return nil, fmt.Errorf("%w: хранилище задач", ErrMissingDependency)
	}
	if publisher == nil {
		return nil, fmt.Errorf("%w: публикатор событий", ErrMissingDependency)
	}
	if clock == nil {
		return nil, fmt.Errorf("%w: часы", ErrMissingDependency)
	}

	return &TaskService{repo: repo, publisher: publisher, clock: clock}, nil
}

// CreateTask создаёт задачу и возвращает её идентификатор.
func (s *TaskService) CreateTask(ctx context.Context, cmd CreateTaskCommand) (todo.TaskID, error) {
	owner, err := todo.ParseOwnerID(cmd.OwnerID)
	if err != nil {
		return todo.TaskID{}, err
	}
	title, err := todo.NewTitle(cmd.Title)
	if err != nil {
		return todo.TaskID{}, err
	}
	description, err := todo.NewDescription(cmd.Description)
	if err != nil {
		return todo.TaskID{}, err
	}
	priority, err := parseOptionalPriority(cmd.Priority)
	if err != nil {
		return todo.TaskID{}, err
	}

	// Момент берётся один раз и служит и сроку, и самой задаче: иначе срок
	// проверялся бы относительно одного «сейчас», а записывался при другом.
	now := s.clock.Now()

	dueDate, err := parseDueDate(cmd.DueDate, now)
	if err != nil {
		return todo.TaskID{}, err
	}
	id, err := todo.NewTaskID()
	if err != nil {
		return todo.TaskID{}, err
	}

	task, err := todo.NewTask(id, owner, title, description, priority, dueDate, now)
	if err != nil {
		return todo.TaskID{}, err
	}

	if err := s.repo.Save(ctx, task); err != nil {
		return todo.TaskID{}, err
	}

	// Задача уже записана, и отказ доставки этого не отменяет — поэтому
	// идентификатор возвращается вместе с ошибкой публикации.
	return id, s.publish(ctx, task.PullEvents())
}

// RenameTask меняет заголовок задачи.
func (s *TaskService) RenameTask(ctx context.Context, cmd RenameTaskCommand) error {
	title, err := todo.NewTitle(cmd.Title)
	if err != nil {
		return err
	}

	return s.mutate(ctx, cmd.TaskID, cmd.OwnerID, func(task *todo.Task, now time.Time) error {
		return task.Rename(title, now)
	})
}

// DescribeTask меняет описание задачи.
func (s *TaskService) DescribeTask(ctx context.Context, cmd DescribeTaskCommand) error {
	description, err := todo.NewDescription(cmd.Description)
	if err != nil {
		return err
	}

	return s.mutate(ctx, cmd.TaskID, cmd.OwnerID, func(task *todo.Task, now time.Time) error {
		return task.Describe(description, now)
	})
}

// ChangePriority меняет приоритет задачи.
//
// В отличие от создания, пустая строка здесь не означает обычный приоритет:
// команда без содержания — ошибка, а не молчаливое согласие на normal.
func (s *TaskService) ChangePriority(ctx context.Context, cmd ChangePriorityCommand) error {
	priority, err := todo.ParsePriority(cmd.Priority)
	if err != nil {
		return err
	}

	return s.mutate(ctx, cmd.TaskID, cmd.OwnerID, func(task *todo.Task, now time.Time) error {
		return task.ChangePriority(priority, now)
	})
}

// RescheduleTask назначает, переносит или снимает срок выполнения.
// Nil в команде снимает срок.
func (s *TaskService) RescheduleTask(ctx context.Context, cmd RescheduleTaskCommand) error {
	return s.mutate(ctx, cmd.TaskID, cmd.OwnerID, func(task *todo.Task, now time.Time) error {
		// Срок разбирается внутри мутатора: он проверяется относительно того
		// же «сейчас», с которым его увидит домен.
		dueDate, err := parseDueDate(cmd.DueDate, now)
		if err != nil {
			return err
		}
		return task.Reschedule(dueDate, now)
	})
}

// StartTask переводит задачу в работу.
func (s *TaskService) StartTask(ctx context.Context, cmd StartTaskCommand) error {
	return s.mutate(ctx, cmd.TaskID, cmd.OwnerID, func(task *todo.Task, now time.Time) error {
		return task.Start(now)
	})
}

// CompleteTask отмечает задачу выполненной.
func (s *TaskService) CompleteTask(ctx context.Context, cmd CompleteTaskCommand) error {
	return s.mutate(ctx, cmd.TaskID, cmd.OwnerID, func(task *todo.Task, now time.Time) error {
		return task.Complete(now)
	})
}

// CancelTask отменяет задачу.
func (s *TaskService) CancelTask(ctx context.Context, cmd CancelTaskCommand) error {
	return s.mutate(ctx, cmd.TaskID, cmd.OwnerID, func(task *todo.Task, now time.Time) error {
		return task.Cancel(now)
	})
}

// mutate — общий скелет всех сценариев, меняющих уже существующую задачу:
// разобрать идентификаторы, поднять агрегат, убедиться в правах на него,
// применить ровно один доменный метод, сохранить и опубликовать события.
//
// Новый сценарий пишется по этому же образцу: разбор своих полей до вызова,
// один вызов домена внутри. Дублировать эти шаги вручную незачем — именно
// так теряются проверка владельца и публикация.
func (s *TaskService) mutate(
	ctx context.Context,
	taskID, ownerID string,
	mutate func(task *todo.Task, now time.Time) error,
) error {
	id, err := todo.ParseTaskID(taskID)
	if err != nil {
		return err
	}
	owner, err := todo.ParseOwnerID(ownerID)
	if err != nil {
		return err
	}

	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if task.OwnerID() != owner {
		return fmt.Errorf("%w: задача %s", ErrForbidden, id)
	}

	if err := mutate(task, s.clock.Now()); err != nil {
		return err
	}

	if err := s.repo.Save(ctx, task); err != nil {
		return err
	}

	return s.publish(ctx, task.PullEvents())
}

// publish отдаёт события публикатору. Неуспешная операция событий не
// порождает, поэтому пустая партия до порта не доходит.
func (s *TaskService) publish(ctx context.Context, events []todo.DomainEvent) error {
	if len(events) == 0 {
		return nil
	}
	if err := s.publisher.Publish(ctx, events); err != nil {
		return fmt.Errorf("app: опубликовать события задачи: %w", err)
	}
	return nil
}

// parseOptionalPriority разбирает приоритет, считая пустую строку обычным:
// нулевое значение todo.Priority осмысленно, и требовать явного "normal"
// от того, кто просто не указал важность, незачем.
func parseOptionalPriority(s string) (todo.Priority, error) {
	if strings.TrimSpace(s) == "" {
		return todo.PriorityNormal, nil
	}
	return todo.ParsePriority(s)
}

// parseDueDate превращает необязательный момент времени в срок выполнения.
// Nil остаётся nil: отсутствие срока — законное состояние задачи.
func parseDueDate(at *time.Time, now time.Time) (*todo.DueDate, error) {
	if at == nil {
		return nil, nil
	}

	dueDate, err := todo.NewDueDate(*at, now)
	if err != nil {
		return nil, err
	}
	return &dueDate, nil
}
