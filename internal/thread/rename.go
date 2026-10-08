package thread

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

// Renaming is metadata, like labels and read state: a store write and the
// list broadcast, with no actor involved, so a thread with no live harness
// can be renamed and every paired device sees the new name.

// maxTitleRunes bounds a title the user typed. Generous next to the 60 a
// prompt-derived title gets: the sidebar truncates anyway, and the header and
// tab show more.
const maxTitleRunes = 200

// ErrEmptyTitle refuses a rename that would leave the thread nameless.
var ErrEmptyTitle = errors.New("a thread title cannot be empty")

// RenameThread gives a thread the title the user chose and returns it as
// stored. The title is folded onto one line, since every place that shows it
// is one line high.
func (m *Manager) RenameThread(ctx context.Context, threadID, title string) (string, error) {
	title = normaliseTitle(title)
	if title == "" {
		return "", ErrEmptyTitle
	}
	if err := m.store.RenameThread(ctx, threadID, title); err != nil {
		return "", err
	}
	m.notifyList()
	return title, nil
}

// normaliseTitle collapses every run of whitespace, newlines included, to a
// single space and caps the length on a rune boundary.
func normaliseTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if utf8.RuneCountInString(title) <= maxTitleRunes {
		return title
	}
	r := []rune(title)
	return strings.TrimSpace(string(r[:maxTitleRunes]))
}
