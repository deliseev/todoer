package todo

import (
	"strings"
	"unicode/utf8"
)

// MaxTitleLength — предельная длина заголовка в рунах, а не в байтах:
// «Купить молоко» должно занимать 13 символов, а не 24.
const MaxTitleLength = 256

// Title — заголовок задачи. Непустой, обрезанный по краям, ограниченный
// по длине.
type Title struct {
	value string
}

// NewTitle нормализует и проверяет заголовок.
func NewTitle(s string) (Title, error) {
	trimTitle := strings.TrimSpace(s)
	if trimTitle == "" {
		return Title{}, ErrEmptyTitle
	}
	if utf8.RuneCountInString(trimTitle) > MaxTitleLength {
		return Title{}, ErrTitleTooLong
	}
	return Title{trimTitle}, nil
}

// String возвращает нормализованный заголовок.
func (t Title) String() string {
	return t.value
}

// IsZero сообщает, что заголовок не был инициализирован.
func (t Title) IsZero() bool {
	return t.value == ""
}
