package session

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// Labels are the user's own workflow markers — pure metadata over sessions.
// None of this goes near an actor: assigning a label needs no harness, must
// work on a session with no live process, and reaches clients through the
// session-list broadcast the sidebar already consumes. The definitions have
// their own broadcast (SubscribeLabels) because they are user-level, owned by
// no one session's log.

// Labels returns every definition in the user's chosen order.
func (m *Manager) Labels(ctx context.Context) ([]store.Label, error) {
	return m.store.ListLabels(ctx)
}

// CreateLabel makes a new definition at the end of the order.
func (m *Manager) CreateLabel(ctx context.Context, name, color string) (store.Label, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return store.Label{}, errors.New("a label needs a name")
	}
	// The store assigns the position inside the INSERT — reading max+1 here
	// would let two devices creating at once claim the same slot.
	label, err := m.store.CreateLabel(ctx, store.Label{
		ID:        uuid.NewString(),
		Name:      name,
		Color:     color,
		CreatedAt: proto.NowMillis(),
	})
	if err != nil {
		return store.Label{}, err
	}
	m.notifyLabels()
	return label, nil
}

// SaveLabel rewrites one definition: rename, recolour, reorder, or the
// collapse default.
func (m *Manager) SaveLabel(ctx context.Context, label store.Label) (store.Label, error) {
	label.Name = strings.TrimSpace(label.Name)
	if label.Name == "" {
		return store.Label{}, errors.New("a label needs a name")
	}
	if err := m.store.SaveLabel(ctx, label); err != nil {
		return store.Label{}, err
	}
	m.notifyLabels()
	return label, nil
}

// DeleteLabel removes a definition and unlabels its sessions; the sessions
// themselves are untouched. Both broadcasts fire: the definition list changed,
// and so did the labelId on every session that carried it.
func (m *Manager) DeleteLabel(ctx context.Context, id string) error {
	if err := m.store.DeleteLabel(ctx, id); err != nil {
		return err
	}
	m.notifyLabels()
	m.notifyList()
	return nil
}

// SetSessionLabel files a session under a label, or "" to clear it. A plain
// store write plus the list broadcast — deliberately not an actor command:
// the set_mode path round-trips to the harness and never writes the row the
// sidebar reads, neither of which fits pure metadata.
func (m *Manager) SetSessionLabel(ctx context.Context, sessionID, labelID string) error {
	if err := m.store.SetSessionLabel(ctx, sessionID, labelID); err != nil {
		return err
	}
	m.notifyList()
	return nil
}
