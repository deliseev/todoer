package app

import (
	"context"
	"crypto/sha256"
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
	// uow — для изменений: задача и её события обязаны записаться вместе.
	uow UnitOfWork
	// repo — для чтения. Читающему сценарию единица работы не нужна: он не
	// пишет, не двигает версию и не порождает событий, а транзакция ради
	// одного SELECT стоила бы дороже, чем даёт.
	repo  Repository
	clock Clock
}

// NewTaskService собирает сервис. Все зависимости обязательны.
func NewTaskService(uow UnitOfWork, repo Repository, clock Clock) (*TaskService, error) {
	if uow == nil {
		return nil, fmt.Errorf("app: build task service (unit of work): %w", ErrMissingDependency)
	}
	if repo == nil {
		return nil, fmt.Errorf("app: build task service (task repository): %w", ErrMissingDependency)
	}
	if clock == nil {
		return nil, fmt.Errorf("app: build task service (clock): %w", ErrMissingDependency)
	}

	return &TaskService{uow: uow, repo: repo, clock: clock}, nil
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
	if err := checkIdempotencyKey(cmd.IdempotencyKey); err != nil {
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

	// Задача, событие о ней и ключ идемпотентности записываются одной работой:
	// либо есть всё, либо не произошло ничего. Разъедься они — повтор,
	// пришедший между двумя записями, создал бы дубль.
	err = s.uow.Do(ctx, func(ctx context.Context, tx Tx) error {
		if cmd.IdempotencyKey != "" {
			stored, replayed, err := tx.Keys().Reserve(ctx, IdempotencyKey{
				OwnerID:     owner,
				Key:         cmd.IdempotencyKey,
				Fingerprint: requestFingerprint(owner, title, description, priority, dueDate),
				TaskID:      id,
			})
			if err != nil {
				return err
			}
			// Повтор: задача создана прошлым запросом, и создавать вторую
			// незачем — вызывающий получит идентификатор первой.
			if replayed {
				id = stored
				return nil
			}
		}

		// Нулевая версия подъёма: задача только что создана и в хранилище
		// ещё не бывала.
		if err := tx.Tasks().Save(ctx, task, 0); err != nil {
			return err
		}
		return tx.Outbox().Add(ctx, task.PullEvents())
	})
	if err != nil {
		return todo.TaskID{}, err
	}

	return id, nil
}

// requestFingerprint считает отпечаток запроса на создание задачи.
//
// Считается по разобранным значениям, а не по телу запроса, и это решение:
// отпечаток тогда одинаков для любого транспорта, а запросы, отличающиеся
// лишними пробелами в заголовке, домен уже свёл к одному — значит и повтором
// они считаются правильно.
//
// Ключ без отпечатка защищал бы только от честного повтора: клиент, пославший
// с тем же ключом другую задачу, получил бы в ответ чужой идентификатор и
// решил, что создал не то, что создал.
func requestFingerprint(
	owner todo.OwnerID,
	title todo.Title,
	description todo.Description,
	priority todo.Priority,
	dueDate *todo.DueDate,
) []byte {
	digest := sha256.New()

	// %q, а не голая склейка: без разделителя и экранирования «аб» + «в»
	// и «а» + «бв» дали бы один отпечаток.
	fmt.Fprintf(digest, "%q\n%q\n%q\n%q\n", owner, title, description, priority)

	if dueDate == nil {
		fmt.Fprint(digest, "-\n")
	} else {
		fmt.Fprintf(digest, "%s\n", dueDate.Time().UTC().Format(time.RFC3339Nano))
	}

	return digest.Sum(nil)
}

// GetTask читает задачу владельца.
//
// Чтение не порождает событий, не двигает версию и не нуждается в блокировке,
// поэтому идёт мимо mutate: общего с изменением у них только подъём задачи,
// и он вынесен в load.
func (s *TaskService) GetTask(ctx context.Context, query GetTaskQuery) (TaskView, error) {
	id, owner, err := parseIdentity(query.TaskID, query.OwnerID)
	if err != nil {
		return TaskView{}, err
	}

	task, _, err := load(ctx, s.repo, id, owner)
	if err != nil {
		return TaskView{}, err
	}

	return newTaskView(task.Snapshot()), nil
}

// newTaskView раскладывает снимок в плоское представление для чтения.
func newTaskView(snapshot todo.TaskSnapshot) TaskView {
	view := TaskView{
		ID:          snapshot.ID.String(),
		OwnerID:     snapshot.OwnerID.String(),
		Title:       snapshot.Title.String(),
		Description: snapshot.Description.String(),
		Status:      snapshot.Status.String(),
		Priority:    snapshot.Priority.String(),
		CreatedAt:   snapshot.CreatedAt,
		UpdatedAt:   snapshot.UpdatedAt,
		Version:     snapshot.Version,
	}
	// Оба необязательных поля копируются, и одинаково. Сегодня снимок приходит
	// из Task.Snapshot(), где указатели уже клонированы, но функция принимает
	// любой TaskSnapshot: первый же порт запросов, собравший снимок мимо
	// агрегата, отдал бы наружу записываемое окно в хранилище — ровно та дыра,
	// ради которой в домене заведён clonePtr.
	if snapshot.DueDate != nil {
		dueDate := snapshot.DueDate.Time()
		view.DueDate = &dueDate
	}
	if snapshot.CompletedAt != nil {
		completedAt := *snapshot.CompletedAt
		view.CompletedAt = &completedAt
	}

	return view
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

// UpdateTask применяет частичное изменение: заданные поля меняются одной
// записью, незаданные остаются как есть.
//
// Единственный сценарий, вызывающий несколько доменных методов подряд.
// Так и задумано: мутации копятся в агрегате, поэтому либо в хранилище
// уезжает вся команда, либо, при отказе на любом поле, ничего.
func (s *TaskService) UpdateTask(ctx context.Context, cmd UpdateTaskCommand) error {
	if cmd.Title == nil && cmd.Description == nil && cmd.Priority == nil && cmd.DueDate == nil {
		return fmt.Errorf("app: update task: %w", ErrEmptyUpdate)
	}

	// Всё, что можно разобрать до похода в хранилище, разбирается здесь:
	// негодная команда не должна стоить чтения. Исключение прежнее — срок,
	// которому нужен тот же «сейчас», что увидит домен.
	var (
		title       todo.Title
		description todo.Description
		priority    todo.Priority
		err         error
	)
	if cmd.Title != nil {
		if title, err = todo.NewTitle(*cmd.Title); err != nil {
			return err
		}
	}
	if cmd.Description != nil {
		if description, err = todo.NewDescription(*cmd.Description); err != nil {
			return err
		}
	}
	if cmd.Priority != nil {
		if priority, err = todo.ParsePriority(*cmd.Priority); err != nil {
			return err
		}
	}

	return s.mutate(ctx, cmd.TaskID, cmd.OwnerID, func(task *todo.Task, now time.Time) error {
		if cmd.Title != nil {
			if err := task.Rename(title, now); err != nil {
				return err
			}
		}
		if cmd.Description != nil {
			if err := task.Describe(description, now); err != nil {
				return err
			}
		}
		if cmd.Priority != nil {
			if err := task.ChangePriority(priority, now); err != nil {
				return err
			}
		}
		if cmd.DueDate != nil {
			dueDate, err := resolveDueDate(task, cmd.DueDate.At, now)
			if err != nil {
				return err
			}
			if err := task.Reschedule(dueDate, now); err != nil {
				return err
			}
		}

		return nil
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
		dueDate, err := resolveDueDate(task, cmd.DueDate, now)
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
	// Идентификаторы разбираются до открытия работы: негодная команда не
	// должна стоить ни чтения, ни начатой транзакции.
	id, owner, err := parseIdentity(taskID, ownerID)
	if err != nil {
		return err
	}

	return s.uow.Do(ctx, func(ctx context.Context, tx Tx) error {
		return s.apply(ctx, tx, id, owner, mutate)
	})
}

// apply — тело изменения внутри уже открытой работы: поднять задачу, проверить
// права, применить домен, сохранить и положить события в очередь.
//
// Отделено от mutate ради читаемости: снаружи видно, что изменение целиком
// происходит в одной работе, внутри — из чего оно состоит.
func (s *TaskService) apply(
	ctx context.Context,
	tx Tx,
	id todo.TaskID,
	owner todo.OwnerID,
	mutate func(task *todo.Task, now time.Time) error,
) error {
	task, loadedVersion, err := load(ctx, tx.Tasks(), id, owner)
	if err != nil {
		return err
	}

	// Доменная ошибка обогащается идентификатором задачи: сентинель говорит,
	// что случилось, но не с чем. Имя сценария в текст не тащим — строка
	// разъедется с именем метода при первом же переименовании, а
	// errors.Is-контракт от обёртки не меняется.
	if err := mutate(task, s.clock.Now()); err != nil {
		return fmt.Errorf("app: mutate task %s: %w", task.ID(), err)
	}

	// Домен на повторе версию не двигает, а двигает её только apply — общий
	// хвост любой состоявшейся мутации. Значит «версия та же, что при
	// подъёме» — это и есть «не произошло ничего», и писать нечего: снимок
	// в хранилище уже такой. Разница не в лишней записи, а в том, что она
	// сверяется с версией: чужая правка, случившаяся между Get и Save,
	// вернула бы вызывающему ErrVersionConflict за изменение, которого он
	// не делал. Правило домена сценарий при этом не дублирует — что считать
	// изменением, решает агрегат, здесь только читается его версия.
	if task.Version() == loadedVersion {
		return nil
	}

	// Версия подъёма едет обратно нетронутой: сценарий её не вычисляет,
	// иначе оптимистичная блокировка зависела бы от его аккуратности.
	if err := tx.Tasks().Save(ctx, task, loadedVersion); err != nil {
		return err
	}

	return tx.Outbox().Add(ctx, task.PullEvents())
}

// parseIdentity разбирает идентификаторы задачи и владельца.
//
// Отдельно от подъёма: разбор идёт до всякой работы с хранилищем, а подъём —
// уже внутри неё. Негодная команда поэтому не стоит ни чтения, ни транзакции.
func parseIdentity(taskID, ownerID string) (todo.TaskID, todo.OwnerID, error) {
	id, err := todo.ParseTaskID(taskID)
	if err != nil {
		return todo.TaskID{}, todo.OwnerID{}, err
	}
	owner, err := todo.ParseOwnerID(ownerID)
	if err != nil {
		return todo.TaskID{}, todo.OwnerID{}, err
	}

	return id, owner, nil
}

// load поднимает задачу владельца из переданного хранилища и сверяет права.
// Общая часть чтения и изменения — и единственное место, где живёт авторизация.
//
// Хранилище приезжает параметром, а не берётся у сервиса: изменение работает
// внутри единицы работы (tx.Tasks()), чтение — мимо неё (s.repo), а проверка
// владельца обязана остаться одна на оба пути. Без общего подъёма следующий
// читающий сценарий списывался бы с GetTask, а однажды списался бы без неё.
// Версия подъёма возвращается всем, но нужна только пишущим — читатель её
// отбрасывает.
func load(ctx context.Context, repo Repository, id todo.TaskID, owner todo.OwnerID) (*todo.Task, int, error) {
	task, loadedVersion, err := repo.Get(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	// Для постороннего задачи не существует. Отказ по владельцу намеренно
	// неотличим от отсутствия задачи: скажи мы «нельзя» вместо «нет»,
	// и перебор идентификаторов начнёт рассказывать, какие из них заняты.
	// Причина остаётся в тексте — для человека, читающего лог, а не для
	// errors.Is.
	if task.OwnerID() != owner {
		return nil, 0, fmt.Errorf("app: task %s belongs to another owner: %w", id, ErrTaskNotFound)
	}

	return task, loadedVersion, nil
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

// MaxIdempotencyKeyLength — потолок длины ключа идемпотентности в байтах.
//
// В байтах, а не в рунах: ключ — непрозрачный жетон, а не человеческий текст,
// и считать в нём буквы незачем. Число взято тем же способом, что и потолки
// домена, — с запасом над законным употреблением: ключом служит UUID или
// хеш, то есть десятки символов, а 255 берут и Stripe, и черновик IETF.
const MaxIdempotencyKeyLength = 255

// checkIdempotencyKey отвергает непомерный ключ до того, как тот доберётся
// до хранилища.
//
// Потолок нужен по той же причине, по какой он есть у заголовка задачи:
// строка приходит от клиента, а уезжает в хранилище — под замок, которым
// разводятся повторы. Без потолка ключ в несколько килобайт возвращался бы
// отказом хранилища, то есть 500 и записью в счётчике 5xx за испорченный
// клиентом запрос. Пустой ключ законен: он означает, что защиты от дубля
// не просили.
func checkIdempotencyKey(key string) error {
	if len(key) > MaxIdempotencyKeyLength {
		return fmt.Errorf("app: check idempotency key (length %d, max %d): %w",
			len(key), MaxIdempotencyKeyLength, ErrIdempotencyKeyTooLong)
	}
	return nil
}

// resolveDueDate превращает присланный момент в срок уже существующей задачи.
//
// Момент, на который срок задачи уже назначен, возвращается как есть и проверку
// на будущее не проходит: это повтор, а не назначение. Без этого круг
// GetTask → UpdateTask разрывался бы на просроченной задаче — TaskView отдаёт
// срок наружу, клиент возвращает форму целиком, и поменять у такой задачи
// нельзя было бы даже заголовок. Отличает повтор от назначения домен
// (HasDueDateAt): сравнивать сроки самостоятельно сценарию нечем и незачем.
func resolveDueDate(task *todo.Task, at *time.Time, now time.Time) (*todo.DueDate, error) {
	if at == nil || !task.HasDueDateAt(*at) {
		return parseDueDate(at, now)
	}

	// HasDueDateAt уже подтвердил, что срок есть.
	current, _ := task.DueDate()
	return &current, nil
}

// parseDueDate превращает необязательный момент времени в срок выполнения.
// Nil остаётся nil: отсутствие срока — законное состояние задачи.
//
// Для существующей задачи звать надо resolveDueDate: здесь проверка на будущее
// безусловна, а повтор собственного срока ей не подчиняется.
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
