package todo

import (
	"strings"
	"unicode/utf8"
)

// MaxDescriptionLength — предельная длина описания в рунах.
const MaxDescriptionLength = 4096

// Description — необязательное подробное описание задачи.
// В отличие от Title, пустое значение допустимо.
type Description struct {
	value string
}

// NewDescription нормализует и проверяет описание.
func NewDescription(s string) (Description, error) {
	trimDescription := strings.TrimSpace(s)
	if utf8.RuneCountInString(trimDescription) > MaxDescriptionLength {
		return Description{}, ErrDescriptionTooLong
	}
	return Description{trimDescription}, nil
}

// String возвращает нормализованное описание.
func (d Description) String() string {
	return d.value
}

// IsEmpty сообщает, что описание не задано.
func (d Description) IsEmpty() bool {
	return d.value == ""
}
