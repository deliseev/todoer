package postgres_test

import (
	"testing"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/app/apptest"
	"github.com/deliseev/todoer/internal/infra/postgres"
	"github.com/deliseev/todoer/internal/infra/postgres/pgtest"
)

// Хранилище на Postgres обязано удовлетворять порту app.Repository.
var _ app.Repository = (*postgres.TaskRepository)(nil)

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

// newRepository поднимает хранилище на свежей базе, созданной из шаблона.
func newRepository(t *testing.T) *postgres.TaskRepository {
	t.Helper()

	pool, err := postgres.Open(t.Context(), pgtest.NewDSN(t))
	if err != nil {
		t.Fatalf("подключение к базе теста: %v", err)
	}
	t.Cleanup(pool.Close)

	return postgres.NewTaskRepository(pool)
}
