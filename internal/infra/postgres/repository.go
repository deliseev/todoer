package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

// Запросы хранилища.
//
// Параметры именованные, а не позиционные: имена сверяются с тегами taskRow,
// и перепутать их местами нечем, тогда как номера приходилось считать руками
// и пересчитывать при каждой новой колонке. Колонки в selectTask перечислены
// явно, а не звёздочкой: разбор по именам требует, чтобы полей структуры было
// ровно столько же, сколько колонок в ответе, и лишняя колонка из будущей
// миграции обязана стать ошибкой, а не сюрпризом.
//
// loaded_version в updateTask — версия, на которой задачу подняли: сверяется
// именно она, а не та, до которой писатель домутировал.
const (
	selectTask = `SELECT id, owner_id, title, description, status, priority,
			due_date, completed_at, created_at, updated_at, version
		FROM tasks WHERE id = @id`

	insertTask = `INSERT INTO tasks (id, owner_id, title, description, status, priority,
			due_date, completed_at, created_at, updated_at, version)
		VALUES (@id, @owner_id, @title, @description, @status, @priority,
			@due_date, @completed_at, @created_at, @updated_at, @version)
		ON CONFLICT (id) DO NOTHING`

	updateTask = `UPDATE tasks SET
			owner_id = @owner_id, title = @title, description = @description,
			status = @status, priority = @priority, due_date = @due_date,
			completed_at = @completed_at, created_at = @created_at,
			updated_at = @updated_at, version = @version
		WHERE id = @id AND version = @loaded_version`

	selectVersion = `SELECT version FROM tasks WHERE id = @id`
)

// TaskRepository — реализация app.Repository поверх Postgres.
//
// Оптимистичную блокировку считает база: обновление сверяет версию в WHERE,
// вставка опирается на первичный ключ. Ноль затронутых строк и означает
// конфликт — сверка и запись происходят одним запросом, поэтому вклиниться
// между ними нечему.
type TaskRepository struct {
	// db — пул или транзакция: запросы у них одни и те же, а в транзакции
	// хранилище работает, когда задачу пишут вместе с её событиями.
	db db
}

// NewTaskRepository собирает хранилище поверх пула соединений.
func NewTaskRepository(pool *pgxpool.Pool) *TaskRepository {
	return &TaskRepository{db: pool}
}

// Get поднимает задачу из базы и сообщает версию, которой она пришла.
func (r *TaskRepository) Get(ctx context.Context, taskID todo.TaskID) (*todo.Task, int, error) {
	// Колонки раскладываются по полям по именам, а не по порядку: сместись
	// список в запросе относительно структуры — будет ошибка о ненайденной
	// колонке, а не тихо переставленные значения.
	rows, err := r.db.Query(ctx, selectTask, pgx.StrictNamedArgs{"id": taskID.String()})
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: get task %s: %w", taskID, err)
	}

	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[taskRow])
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, 0, fmt.Errorf("postgres: get task %s: %w", taskID, app.ErrTaskNotFound)
	case err != nil:
		return nil, 0, fmt.Errorf("postgres: get task %s: %w", taskID, err)
	}

	snapshot, err := row.snapshot()
	if err != nil {
		return nil, 0, err
	}

	// Восстановление проверяет то, что обещано вызывающему: Get отдаёт
	// валидный агрегат. Строка в базе могла прийти откуда угодно, поэтому
	// проверка здесь не формальность — в отличие от хранилища в памяти,
	// куда попадают только снимки живых агрегатов.
	task, err := todo.ReconstituteTask(snapshot)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: reconstitute task %s (%v): %w", taskID, err, errCorruptRow)
	}

	return task, snapshot.Version, nil
}

// Save сохраняет задачу, соблюдая оптимистичную блокировку по версии подъёма.
func (r *TaskRepository) Save(ctx context.Context, task *todo.Task, loadedVersion int) error {
	// Аргумент проверяется раньше контекста: нарушение контракта остаётся
	// нарушением независимо от того, ждёт ли ещё кто-то ответа.
	if task == nil {
		return errNilTask
	}
	// Отменённому запросу незачем занимать соединение, чтобы узнать, что он
	// никому не нужен.
	if err := ctx.Err(); err != nil {
		return err
	}

	row := newTaskRow(task.Snapshot())

	// Версия подъёма, равная нулю, означает «в хранилище не бывала»: у живой
	// задачи версия всегда не меньше единицы. Отсюда и два разных запроса —
	// вставка и обновление, — а не один UPSERT: слить их значило бы позволить
	// вставке затирать чужую задачу.
	if loadedVersion == 0 {
		return r.insert(ctx, task.ID(), row)
	}

	return r.update(ctx, task.ID(), row, loadedVersion)
}

// insert вставляет задачу, которой в хранилище ещё не было.
func (r *TaskRepository) insert(ctx context.Context, taskID todo.TaskID, row taskRow) error {
	tag, err := r.db.Exec(ctx, insertTask, row.args())
	if err != nil {
		return fmt.Errorf("postgres: insert task %s: %w", taskID, err)
	}

	if tag.RowsAffected() == 0 {
		// Писатель считает задачу новой, а место уже занято.
		return fmt.Errorf("postgres: insert task %s (%s): %w",
			taskID, r.storedVersion(ctx, taskID), app.ErrVersionConflict)
	}

	return nil
}

// update перезаписывает задачу, поднятую из хранилища на версии loadedVersion.
func (r *TaskRepository) update(ctx context.Context, taskID todo.TaskID, row taskRow, loadedVersion int) error {
	args := row.args()
	// Единственный параметр, которого нет в строке: версия подъёма живёт на
	// границе, а не в задаче.
	args["loaded_version"] = loadedVersion

	tag, err := r.db.Exec(ctx, updateTask, args)
	if err != nil {
		return fmt.Errorf("postgres: save task %s: %w", taskID, err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: save task %s (loaded version %d, %s): %w",
			taskID, loadedVersion, r.storedVersion(ctx, taskID), app.ErrVersionConflict)
	}

	return nil
}

// storedVersion описывает словами, что лежит в базе на месте задачи.
//
// Нужно только ради текста ошибки: «ноль затронутых строк» база объясняет
// одинаково и когда версии разошлись, и когда задачи там больше нет, а разница
// между этими случаями читается в логе, когда уже всё сломалось. На решение
// уточнение не влияет — отказ случился раньше, — и на горячий путь не
// попадает: этот запрос делается только на конфликте.
//
// Ответ может успеть устареть, пока идёт этот запрос, и это не беда: он
// описывает не исход операции, а обстановку вокруг неё.
func (r *TaskRepository) storedVersion(ctx context.Context, taskID todo.TaskID) string {
	var version int

	err := r.db.QueryRow(ctx, selectVersion, pgx.StrictNamedArgs{"id": taskID.String()}).Scan(&version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "not stored anymore"
	case err != nil:
		return "stored version unknown"
	}

	return fmt.Sprintf("stored version %d", version)
}
