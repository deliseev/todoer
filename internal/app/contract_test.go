package app_test

import (
	"testing"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/app/apptest"
)

// Двойники обязаны удовлетворять портам, которые подменяют.
var (
	_ app.Repository = (*fakeRepository)(nil)
	_ app.UnitOfWork = (*fakeUnitOfWork)(nil)
	_ app.Outbox     = (*fakeOutbox)(nil)
	_ app.Tx         = fakeTx{}
)

// TestFakeRepositoryContract прогоняет по двойнику тот же набор, что и по
// настоящему хранилищу.
//
// Упрощённый двойник перестаёт ловить то, на чём споткнётся оригинал: правила
// блокировки здесь обязаны совпадать с настоящими, иначе тесты сценариев
// проверяют выдуманное хранилище. Средства самого двойника — подменённые
// ошибки, хук перед записью, счётчик записей — в набор не входят и остаются
// его собственным делом.
func TestFakeRepositoryContract(t *testing.T) {
	apptest.RepositoryContract(t, func(*testing.T) app.Repository {
		return newFakeRepository()
	})
}

// TestFakeUnitOfWorkContract прогоняет по двойнику набор единицы работы.
//
// Атомарность двойник изображает снимком состояния, настоящая единица работы
// добивается её транзакцией — и разойтись эти способы обязаны только
// устройством. Всё, на что вправе рассчитывать сценарий, набор проверяет
// у обоих одинаково.
func TestFakeUnitOfWorkContract(t *testing.T) {
	apptest.UnitOfWorkContract(t, func(*testing.T) apptest.UnitOfWorkFixture {
		outbox := newFakeOutbox()

		return apptest.UnitOfWorkFixture{
			UnitOfWork:   newFakeUnitOfWork(newFakeRepository(), outbox),
			QueuedEvents: func(*testing.T) []string { return outbox.queued() },
		}
	})
}

// TestFakeIdempotencyContract прогоняет по двойнику набор хранилища ключей.
func TestFakeIdempotencyContract(t *testing.T) {
	apptest.IdempotencyContract(t, func(*testing.T) app.UnitOfWork {
		return newFakeUnitOfWork(newFakeRepository(), newFakeOutbox())
	})
}
