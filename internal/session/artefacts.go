package session

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/artefact"
	"github.com/asiraky/omniplex/internal/proto"
)

// ToolServers returns the MCP servers omniplex runs beside a session's
// harness. Set once at startup, before any session starts; nil means none.
// A package variable rather than a manager field because every path that
// starts a harness (create, resume, activate) needs it and none of them has
// the manager.
var ToolServers func(sessionID string) []adapter.MCPServer

func toolServers(sessionID string) []adapter.MCPServer {
	if ToolServers == nil {
		return nil
	}
	return ToolServers(sessionID)
}

// Publish is a request to attach staged bytes to the session as an artefact.
type Publish struct {
	Staged *artefact.Staged
	Name   string
	Source string
	Note   string
}

var ErrNoName = errors.New("an artefact needs a name")

// PublishArtefact commits staged bytes as a version of an artefact. A name
// the session already has becomes that artefact's next version; the decision
// is made inside the actor so two publishes of one name cannot both be
// version 2.
func (a *Actor) PublishArtefact(ctx context.Context, p Publish) (proto.ArtefactPublishedPayload, error) {
	if p.Name == "" {
		return proto.ArtefactPublishedPayload{}, ErrNoName
	}
	v, err := a.call(ctx, command{kind: cmdPublishArtefact, publish: &p})
	if err != nil {
		return proto.ArtefactPublishedPayload{}, err
	}
	return v.(proto.ArtefactPublishedPayload), nil
}

func (a *Actor) handlePublish(p *Publish) (proto.ArtefactPublishedPayload, error) {
	id, version := uuid.NewString(), 1
	if existing, ok := a.state.ArtefactByName(p.Name); ok {
		id, version = existing.ID, existing.Latest().Version+1
	}
	if err := p.Staged.Commit(id, version); err != nil {
		return proto.ArtefactPublishedPayload{}, err
	}
	payload := proto.ArtefactPublishedPayload{
		ArtefactID: id, Version: version, Name: p.Name, MediaType: p.Staged.MediaType, Size: p.Staged.Size,
		Entry: p.Staged.Entry, Files: p.Staged.Files, Source: p.Source, Note: p.Note,
	}
	if t := a.lastTurn(); t != nil && !t.Done {
		payload.TurnID = t.ID
	}
	if err := a.append(proto.Emit(proto.ArtefactPublished, payload)); err != nil {
		return proto.ArtefactPublishedPayload{}, err
	}
	return payload, nil
}
