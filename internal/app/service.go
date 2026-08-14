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

	// Нулевая версия подъёма: задача только что создана и в хранилище
	// ещё не бывала.
	if err := s.repo.Save(ctx, task, 0); err != nil {
		return todo.TaskID{}, err
	}

	// Задача уже записана, и отказ доставки этого не отменяет — поэтому
	// идентификатор возвращается вместе с ошибкой публикации.
	return id, s.publish(ctx, id, task.PullEvents())
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
		// Единственный сценарий, разбирающий своё поле внутри мутатора, а не
		// до него: срок обязан проверяться относительно того же «сейчас»,
		// с которым его увидит домен. Плата — лишний поход в хранилище,
		// когда срок заведомо негоден; она сознательная.
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

	task, loadedVersion, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	// Для постороннего задачи не существует. Отказ по владельцу намеренно
	// неотличим от отсутствия задачи: скажи мы «нельзя» вместо «нет»,
	// и перебор идентификаторов начнёт рассказывать, какие из них заняты.
	// Причина остаётся в тексте — для человека, читающего лог, а не для
	// errors.Is.
	if task.OwnerID() != owner {
		return fmt.Errorf("%w: задача %s принадлежит другому владельцу", ErrTaskNotFound, id)
	}

	// Доменная ошибка обогащается идентификатором задачи: сентинель говорит,
	// что случилось, но не с чем. Имя сценария в текст не тащим — строка
	// разъедется с именем метода при первом же переименовании, а
	// errors.Is-контракт от обёртки не меняется.
	if err := mutate(task, s.clock.Now()); err != nil {
		return fmt.Errorf("app: изменить задачу %s: %w", id, err)
	}

	// Версия подъёма едет обратно нетронутой: сценарий её не вычисляет,
	// иначе оптимистичная блокировка зависела бы от его аккуратности.
	if err := s.repo.Save(ctx, task, loadedVersion); err != nil {
		return err
	}

	return s.publish(ctx, id, task.PullEvents())
}

// publish отдаёт события публикатору. Неуспешная операция событий не
// порождает, поэтому пустая партия до порта не доходит.
//
// Отказ доставки возвращается как EventDeliveryError: к этому моменту задача
// уже записана, и вызывающему нужно отличать «изменения нет» от «изменение
// состоялось, но о нём не узнали». Недоставленные события уезжают в ошибке —
// больше их взять неоткуда, буфер агрегата уже пуст.
//
// Повторы и прочая политика доставки — дело реализации EventPublisher,
// а не сценария: сценарий не знает ни во что публикует, ни сколько это стоит.
func (s *TaskService) publish(ctx context.Context, taskID todo.TaskID, events []todo.DomainEvent) error {
	if len(events) == 0 {
		return nil
	}
	// Отмена запроса доставку не отменяет: задача уже записана, и рассказать
	// о ней обязаны независимо от того, ждёт ли ещё ответа тот, кто её
	// заказал. WithoutCancel убирает только отмену и срок, оставляя значения
	// контекста — трассировку и всё, что по нему передают.
	//
	// Собственный срок на доставку — забота реализации: сценарий не знает,
	// сколько она стоит, и вешать сюда произвольный таймаут не станет.
	if err := s.publisher.Publish(context.WithoutCancel(ctx), events); err != nil {
		return &EventDeliveryError{TaskID: taskID, Events: events, Err: err}
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
