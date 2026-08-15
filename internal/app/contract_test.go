package app_test

import (
	"testing"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/app/apptest"
)

// Двойник обязан удовлетворять порту, который подменяет.
var _ app.Repository = (*fakeRepository)(nil)

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
