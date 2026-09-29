package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/FiveTechSoft/FiveAgent/internal/identity"
)

// LinkChannel starts the explicit cross-channel linking flow (stage
// 36): it mints a one-time code for the current channel identity; the
// user writes "vincular <code>" from the other channel to link them.
// Never guessed: without this flow every sender stays separate.
type LinkChannel struct{ IDs *identity.Store }

func (t LinkChannel) Name() string { return "link_channel" }
func (t LinkChannel) Description() string {
	return "Link this chat with the same user's other channels (one conversation and one memory across WhatsApp, Telegram, ...). It mints a one-time code: the user must write 'vincular <code>' from the other channel within 10 minutes. Use it only when the user asks to unify their channels."
}
func (t LinkChannel) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t LinkChannel) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	channel, userID := RequestInfo(ctx)
	if userID == "" {
		return "", errors.New("link_channel: no user in the request context")
	}
	code, err := t.IDs.NewCode(channel, userID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Desde tu otro canal escríbeme exactamente: vincular %s (caduca en 10 minutos, un solo uso)", code), nil
}

// UnlinkChannel dissolves the current identity's links (stage 36).
type UnlinkChannel struct{ IDs *identity.Store }

func (t UnlinkChannel) Name() string { return "unlink_channel" }
func (t UnlinkChannel) Description() string {
	return "Undo this user's channel linking: every channel goes back to its own separate conversation and memory."
}
func (t UnlinkChannel) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t UnlinkChannel) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	channel, userID := RequestInfo(ctx)
	if userID == "" {
		return "", errors.New("unlink_channel: no user in the request context")
	}
	ok, err := t.IDs.Unlink(channel, userID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "this identity has no links", nil
	}
	return "channels unlinked; each one is a separate conversation again", nil
}

// LinkedChannels lists the identities linked with the current one
// (stage 36 audit).
type LinkedChannels struct{ IDs *identity.Store }

func (t LinkedChannels) Name() string { return "linked_channels" }
func (t LinkedChannels) Description() string {
	return "List the channel identities linked with this user's (the ones sharing this conversation and memory)."
}
func (t LinkedChannels) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t LinkedChannels) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	channel, userID := RequestInfo(ctx)
	if userID == "" {
		return "", errors.New("linked_channels: no user in the request context")
	}
	linked := t.IDs.Linked(channel, userID)
	if len(linked) == 0 {
		return "no linked channels; every channel is a separate conversation", nil
	}
	return "linked identities: " + strings.Join(linked, ", "), nil
}
