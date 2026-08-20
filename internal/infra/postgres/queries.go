package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deliseev/todoer/internal/app"
)

// listTasksColumns — колонки страницы списка.
//
// Тот же список, что у selectTask, и по той же причине перечислен явно:
// разбор по именам требует, чтобы полей структуры было ровно столько же,
// сколько колонок в ответе, и лишняя колонка из будущей миграции обязана
// стать ошибкой, а не сюрпризом. Ранга среди них нет намеренно: он пишется,
// но не читается — приоритет поднимается из имени, и единственным носителем
// порядка важности остаётся домен.
const listTasksColumns = `id, owner_id, title, description, status, priority,
		due_date, completed_at, created_at, updated_at, version`

// afterKey — имя параметра, под которым едет ключ сортировки из курсора.
// Значение у него разного типа при разных сортировках, а место одно.
const afterKey = "after_key"

// sortKey — как устроен один порядок выдачи.
//
// Таблицей, а не набором switch по сортировке: носитель у правила один,
// и новая сортировка — это одна строка здесь, а не три ветки в разных местах.
type sortKey struct {
	// expr — выражение ключа над строкой таблицы. Оно же стоит в индексе,
	// иначе страница по курсору перебирала бы всё.
	expr string
	// cursorExpr — то же значение, приехавшее из курсора параметром.
	// Приведение типа явное: у параметра нет колонки, из которой база
	// вывела бы тип сама.
	cursorExpr string
	// cursorValue достаёт из курсора то, что уедет в cursorExpr. Функцией,
	// потому что тип значения свой у каждой сортировки.
	cursorValue func(app.TaskCursor) any
}

// sortKeys — единственный источник правды о том, чем сортируется список.
//
// Порядок по сроку считается по coalesce(due_date, 'infinity'): NULL ломает
// и сравнение кортежей курсора, и место задач без срока в выдаче, а
// 'infinity' даёт им определённое место в конце и сравнимое значение.
//
// Порядок по важности — по priority_rank, а не по имени: алфавит поставил бы
// critical раньше high. Расписывать важность выражением в запросе нельзя —
// у доменного правила стал бы второй носитель.
var sortKeys = [...]sortKey{
	app.SortByDueDate: {
		expr:        `coalesce(due_date, 'infinity'::timestamptz)`,
		cursorExpr:  `coalesce(@` + afterKey + `::timestamptz, 'infinity'::timestamptz)`,
		cursorValue: func(cursor app.TaskCursor) any { return cursor.DueDate },
	},
	app.SortByPriority: {
		expr:        `priority_rank`,
		cursorExpr:  `@` + afterKey + `::smallint`,
		cursorValue: func(cursor app.TaskCursor) any { return cursor.PriorityRank },
	},
	app.SortByCreatedAt: {
		expr:        `created_at`,
		cursorExpr:  `@` + afterKey + `::timestamptz`,
		cursorValue: func(cursor app.TaskCursor) any { return cursor.CreatedAt },
	},
}

// TaskQueries — реализация app.TaskQueries поверх Postgres.
//
// Отдельно от TaskRepository, как и порт от порта: здесь нет ни версии, ни
// блокировки, ни агрегата — наружу уезжает плоское представление. Общее у них
// только отображение строки таблицы, и оно живёт в одном месте, в rows.go.
type TaskQueries struct {
	db db
}

// NewTaskQueries собирает сторону чтения поверх пула соединений.
//
// Транзакция ей не нужна и не предлагается: список ничего не пишет, не
// двигает версий и не порождает событий.
func NewTaskQueries(pool *pgxpool.Pool) *TaskQueries {
	return &TaskQueries{db: pool}
}

// List отдаёт страницу задач владельца в порядке, заданном фильтром.
func (q *TaskQueries) List(ctx context.Context, filter app.TaskFilter) ([]app.TaskView, error) {
	sql, args, err := buildListQuery(filter)
	if err != nil {
		return nil, err
	}

	rows, err := q.db.Query(ctx, sql, args)
	if err != nil {
		return nil, fmt.Errorf("postgres: list tasks of %s: %w", filter.OwnerID, err)
	}

	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[taskRow])
	if err != nil {
		return nil, fmt.Errorf("postgres: list tasks of %s: %w", filter.OwnerID, err)
	}

	views := make([]app.TaskView, 0, len(list))
	for _, row := range list {
		// Строки проверяются теми же фабриками, что и при подъёме одной
		// задачи: строку в базе мог написать кто угодно, и отличить
		// испорченную от негодного запроса клиента может только хранилище.
		// Отказ здесь — пятисотка на весь список, и это честнее, чем отдать
		// клиенту страницу с дырой на месте испорченной задачи.
		snapshot, err := row.snapshot()
		if err != nil {
			return nil, err
		}

		views = append(views, app.NewTaskView(snapshot))
	}

	return views, nil
}

// buildListQuery собирает запрос страницы и его параметры.
//
// Условия и параметры растут вместе, одной строкой кода на каждое: строгие
// именованные аргументы отвергают и лишний параметр, и недостающий, поэтому
// разъехаться этим двум спискам нечем — забытое условие или забытое значение
// становятся ошибкой до того, как запрос уедет в базу.
func buildListQuery(filter app.TaskFilter) (string, pgx.StrictNamedArgs, error) {
	if int(filter.Sort) >= len(sortKeys) {
		return "", nil, fmt.Errorf("postgres: list tasks of %s (sort %d)", filter.OwnerID, filter.Sort)
	}
	key := sortKeys[filter.Sort]

	// Владелец — не просто ещё одно условие: чужие задачи не попадают
	// в выдачу никогда, и обойти его хранилищу нечем.
	conditions := []string{"owner_id = @owner_id"}
	args := pgx.StrictNamedArgs{
		"owner_id": filter.OwnerID.String(),
		"limit":    filter.Limit,
	}

	if filter.Status != nil {
		conditions = append(conditions, "status = @status")
		args["status"] = filter.Status.String()
	}
	if filter.Priority != nil {
		conditions = append(conditions, "priority = @priority")
		args["priority"] = filter.Priority.String()
	}
	// Полуинтервал [from, to): нижняя граница включающая, верхняя нет.
	// Задачи без срока сюда не попадают сами — сравнение с NULL не истинно.
	if filter.DueFrom != nil {
		conditions = append(conditions, "due_date >= @due_from")
		args["due_from"] = *filter.DueFrom
	}
	if filter.DueTo != nil {
		conditions = append(conditions, "due_date < @due_to")
		args["due_to"] = *filter.DueTo
	}

	// Направление одно на весь порядок — и на ключ, и на разрешение ничьих.
	// Переверни мы только ключ, задачи с одинаковым сроком поехали бы по
	// возрастанию идентификатора внутри убывающей выдачи, и курсор через них
	// не прошёл бы.
	direction, beyond := "ASC", ">"
	if filter.Descending {
		direction, beyond = "DESC", "<"
	}

	if filter.After != nil {
		// Сравнение кортежей, а не пара условий через OR: так его понимает
		// индекс — это условие поиска, а не отбор после чтения, ради чего
		// курсор и выбран вместо OFFSET.
		conditions = append(conditions, fmt.Sprintf("(%s, id) %s (%s, @after_id)",
			key.expr, beyond, key.cursorExpr))
		args[afterKey] = key.cursorValue(*filter.After)
		args["after_id"] = filter.After.ID.String()
	}

	// Идентификатор в хвосте порядка обязателен: любые два ключа могут
	// совпасть, и без него у страницы не было бы однозначной точки
	// продолжения — выдача менялась бы от запроса к запросу, теряя и повторяя
	// задачи.
	sql := fmt.Sprintf(`SELECT %s FROM tasks WHERE %s ORDER BY %s %s, id %s LIMIT @limit`,
		listTasksColumns, strings.Join(conditions, " AND "),
		key.expr, direction, direction)

	return sql, args, nil
}
