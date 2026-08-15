package infra_test

import (
	"testing"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/app/apptest"
	"github.com/deliseev/todoer/internal/infra"
)

// Хранилище в памяти обязано удовлетворять порту app.Repository.
var _ app.Repository = (*infra.InMemoryTaskRepository)(nil)

// TestInMemoryTaskRepositoryContract прогоняет по хранилищу общий набор
// порта: реализаций у app.Repository уже две, и поведение у них обязано
// совпадать. Что здесь остаётся от собственных тестов хранилища — только
// то, чего порт не обещает.
func TestInMemoryTaskRepositoryContract(t *testing.T) {
	apptest.RepositoryContract(t, func(*testing.T) app.Repository {
		return infra.NewInMemoryTaskRepository()
	})
}
