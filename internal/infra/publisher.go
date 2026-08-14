package infra

import (
	"context"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// NopPublisher — реализация app.EventPublisher, молча отбрасывающая события.
//
// Нужна там, где доставка ещё не подключена: собирать сервис с nil вместо
// публикатора запрещено, потому что это тихая потеря событий, а тихая потеря
// хуже честной заглушки, про которую все знают.
type NopPublisher struct{}

// Publish ничего не делает и всегда возвращает nil.
func (NopPublisher) Publish(context.Context, []todo.DomainEvent) error {
	return nil
}
