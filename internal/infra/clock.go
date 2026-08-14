// Package infra содержит реализации портов application-слоя: всё, что
// разговаривает с внешним миром — часами, хранилищами, шиной событий.
//
// Зависимость направлена внутрь: инфраструктура знает про app и todo,
// обратного знания нет и не будет.
package infra

import "time"

// SystemClock — реализация app.Clock поверх системных часов.
type SystemClock struct{}

// Now возвращает текущее время в UTC.
func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}
