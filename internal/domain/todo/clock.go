package todo

import "time"

// Clock — порт для получения текущего времени.
//
// Сам агрегат Task часами не пользуется: каждый мутирующий метод принимает
// момент времени параметром now. Порт нужен вызывающей стороне
// (application-слою), чтобы её тоже можно было тестировать детерминированно.
type Clock interface {
	Now() time.Time
}

// SystemClock — реализация Clock поверх системных часов.
type SystemClock struct{}

// Now возвращает текущее время в UTC.
func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}
