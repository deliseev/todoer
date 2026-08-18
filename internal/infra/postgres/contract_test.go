package postgres_test

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/app/apptest"
	"github.com/deliseev/todoer/internal/infra/postgres"
	"github.com/deliseev/todoer/internal/infra/postgres/pgtest"
)

// Реализации обязаны удовлетворять портам слоя сценариев.
var (
	_ app.Repository       = (*postgres.TaskRepository)(nil)
	_ app.UnitOfWork       = (*postgres.UnitOfWork)(nil)
	_ app.Outbox           = (*postgres.Outbox)(nil)
	_ app.IdempotencyStore = (*postgres.IdempotencyStore)(nil)
)

// TestMain готовит шаблонную базу на весь пакет.
func TestMain(m *testing.M) {
	pgtest.Run(m)
}

// TestTaskRepositoryContract прогоняет по хранилищу общий набор порта.
//
// Реализаций у app.Repository теперь три — память, двойник в тестах сценариев
// и эта, — и совпадать они обязаны поведением, а не устройством. Набор здесь
// впервые проверяет параллельность по-настоящему: до сих пор конкурентные
// подтесты сталкивались на мьютексе внутри процесса, а теперь сталкиваются
// в базе, где оптимистичную блокировку считает она сама.
func TestTaskRepositoryContract(t *testing.T) {
	apptest.RepositoryContract(t, func(t *testing.T) app.Repository {
		return newRepository(t)
	})
}

// TestUnitOfWorkContract прогоняет по единице работы общий набор порта.
//
// Здесь атомарность проверяется по-настоящему: двойник изображает её снимком
// состояния, а тут она держится на транзакции, и откат делает база.
func TestUnitOfWorkContract(t *testing.T) {
	apptest.UnitOfWorkContract(t, func(t *testing.T) apptest.UnitOfWorkFixture {
		pool := newPool(t)

		return apptest.UnitOfWorkFixture{
			UnitOfWork:   postgres.NewUnitOfWork(pool),
			QueuedEvents: func(t *testing.T) []string { return queuedEvents(t, pool) },
		}
	})
}

// queuedEvents читает имена событий из очереди в порядке записи.
//
// Запросом на месте, а не через порт: очередь читает доставщик, и заводить
// ради теста метод, которым сценарий не пользуется, незачем.
func queuedEvents(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()

	rows, err := pool.Query(t.Context(), "SELECT name FROM outbox ORDER BY id")
	if err != nil {
		t.Fatalf("чтение очереди: %v", err)
	}

	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("чтение очереди: %v", err)
	}

	return names
}

// TestIdempotencyContract прогоняет по хранилищу ключей общий набор порта.
//
// Здесь набор впервые проверяет замок по-настоящему: двойник разводит
// параллельные повторы мьютексом внутри процесса, а тут они сталкиваются
// на уникальном индексе в базе.
func TestIdempotencyContract(t *testing.T) {
	apptest.IdempotencyContract(t, func(t *testing.T) app.UnitOfWork {
		return postgres.NewUnitOfWork(newPool(t))
	})
}

// newRepository поднимает хранилище на свежей базе, созданной из шаблона.
func newRepository(t *testing.T) *postgres.TaskRepository {
	t.Helper()

	return postgres.NewTaskRepository(newPool(t))
}

// newPool открывает пул к свежей базе, созданной из шаблона.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := postgres.Open(t.Context(), pgtest.NewDSN(t))
	if err != nil {
		t.Fatalf("подключение к базе теста: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}
