package datastore

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/theimaginaryfoundation/what-iff/ent"
	entchat "github.com/theimaginaryfoundation/what-iff/ent/chat"
	"github.com/theimaginaryfoundation/what-iff/ent/memory"
	"github.com/theimaginaryfoundation/what-iff/ent/user"
	"github.com/theimaginaryfoundation/what-iff/internal/i18n"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// Moving a memory across the thread boundary (POST /memory/{id}/scope).
//
// The record itself moves: its id, content, embedding, confidence, provenance and merge lineage
// stay as they are, so a move never creates a duplicate and needs no re-embedding (retrieval
// filters by scope, pin and chat edge, never by the vector; see relatedMemoryReadable). The chat
// edge doubles as provenance (memoryOfChat): a thread memory promoted to global or personality
// keeps the edge to the thread it came from, which is where "back to thread" returns it by default.
// The edge is cleared only when that thread is deleted, and then the memory has no origin.
//
// Personalities are not crossed. A thread memory promoted to personality is pinned to its thread's
// personality. A memory moved back to a thread must land in a thread of its personality: its pin,
// else its origin thread's personality; an unpinned memory with no origin may go to any thread.
// A sandboxed thread takes back only its own memories, since an account memory moved into it would
// become readable to the people in that thread.

// memoryScopeTarget is where a move puts a memory: scope, chat edge and pin, all written.
type memoryScopeTarget struct {
	scope  memory.Scope
	chatID *uuid.UUID
	pinned *uuid.UUID
}

// MoveMemoryScope moves one of userID's memories to move.Level and records the move in the audit
// log. A move to where the memory already is returns it unchanged.
func (d *Datastore) MoveMemoryScope(ctx context.Context, userID, memoryID uuid.UUID, move models.MemoryScopeMove) (*models.Memory, error) {
	tx, err := d.dbClient.Tx(ctx)
	if err != nil {
		d.logger.Error(i18n.T("tx.start_failed"), zap.Error(err))
		return nil, err
	}
	defer func() {
		if v := recover(); v != nil {
			tx.Rollback()
			panic(v)
		}
	}()

	existing, err := tx.Memory.Query().
		Where(memory.ID(memoryID), memory.HasOwnerWith(user.ID(userID))).
		WithChat(func(q *ent.ChatQuery) { q.WithPersonality() }).
		Only(ctx)
	if err != nil {
		tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, ErrMemoryNotFound
		}
		return nil, err
	}

	target, err := d.memoryScopeMoveTarget(ctx, tx, userID, existing, move)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if target.matches(existing) {
		tx.Rollback()
		return toMemoryModel(existing), nil
	}

	update := tx.Memory.UpdateOneID(memoryID).
		SetScope(target.scope).
		SetUpdatedAt(time.Now())
	if target.pinned != nil {
		update.SetPinnedPersonalityID(*target.pinned)
	} else {
		update.ClearPinnedPersonalityID()
	}
	if target.chatID != nil {
		update.SetChatID(*target.chatID)
	} else {
		update.ClearChat()
	}
	if _, err := update.Save(ctx); err != nil {
		tx.Rollback()
		return nil, err
	}

	moved, err := tx.Memory.Query().Where(memory.ID(memoryID)).WithChat().Only(ctx)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		d.logger.Error(i18n.T("tx.commit_failed"), zap.Error(err))
		return nil, err
	}

	d.auditMemoryScopeMove(ctx, userID, existing, moved)
	return toMemoryModel(moved), nil
}

// memoryScopeMoveTarget validates move against the memory and resolves where it lands.
func (d *Datastore) memoryScopeMoveTarget(ctx context.Context, tx *ent.Tx, userID uuid.UUID, existing *ent.Memory, move models.MemoryScopeMove) (memoryScopeTarget, error) {
	from := memoryLevelForEntity(existing)
	if from == models.MemoryLevelSummary {
		return memoryScopeTarget{}, fmt.Errorf("%w: a thread summary stays with its thread", ErrMemoryScopeMove)
	}
	origin := memoryChatID(existing)

	switch move.Level {
	case models.MemoryLevelGlobal:
		if move.ChatID != nil || move.PersonalityID != nil {
			return memoryScopeTarget{}, fmt.Errorf("%w: a move to global takes no chat_id or personality_id", ErrInvalidRequestBody)
		}
		return memoryScopeTarget{scope: memory.ScopeUser, chatID: origin}, nil

	case models.MemoryLevelPersonality:
		if move.ChatID != nil {
			return memoryScopeTarget{}, fmt.Errorf("%w: a move to personality takes no chat_id", ErrInvalidRequestBody)
		}
		personalityID := move.PersonalityID
		if from == models.MemoryLevelThread {
			threadPersonality := memoryChatPersonalityID(existing)
			switch {
			case threadPersonality != nil && personalityID == nil:
				personalityID = threadPersonality
			case threadPersonality != nil && *personalityID != *threadPersonality:
				return memoryScopeTarget{}, fmt.Errorf("%w: a thread memory can only be pinned to its thread's personality", ErrMemoryScopeMove)
			}
		}
		if personalityID == nil || *personalityID == uuid.Nil {
			return memoryScopeTarget{}, fmt.Errorf("%w: choose the personality to pin the memory to", ErrMemoryScopeMove)
		}
		owned, err := d.personalityOwnedByUser(ctx, tx, userID, *personalityID)
		if err != nil {
			return memoryScopeTarget{}, err
		}
		if !owned {
			return memoryScopeTarget{}, ErrPersonalityNotFound
		}
		return memoryScopeTarget{scope: memory.ScopeUser, chatID: origin, pinned: personalityID}, nil

	case models.MemoryLevelThread:
		if move.PersonalityID != nil {
			return memoryScopeTarget{}, fmt.Errorf("%w: a move to thread takes no personality_id", ErrInvalidRequestBody)
		}
		if from == models.MemoryLevelThread {
			return memoryScopeTarget{}, fmt.Errorf("%w: it is already a thread memory", ErrMemoryScopeMove)
		}
		// Never defaulted, not even to the origin: the caller names the thread.
		if move.ChatID == nil || *move.ChatID == uuid.Nil {
			return memoryScopeTarget{}, fmt.Errorf("%w: choose the thread to move the memory to", ErrMemoryScopeMove)
		}
		dest, err := tx.Chat.Query().
			Where(entchat.ID(*move.ChatID), entchat.HasOwnerWith(user.ID(userID))).
			WithPersonality().
			Only(ctx)
		if ent.IsNotFound(err) {
			return memoryScopeTarget{}, ErrChatNotFound
		}
		if err != nil {
			return memoryScopeTarget{}, err
		}
		affinity := existing.PinnedPersonalityID
		if affinity == nil {
			affinity = memoryChatPersonalityID(existing)
		}
		if affinity != nil && (dest.Edges.Personality == nil || dest.Edges.Personality.ID != *affinity) {
			return memoryScopeTarget{}, fmt.Errorf("%w: that thread belongs to a different personality", ErrMemoryScopeMove)
		}
		isOrigin := origin != nil && *origin == dest.ID
		if dest.ContextScope == entchat.ContextScopeSandbox && !isOrigin {
			return memoryScopeTarget{}, fmt.Errorf("%w: a sandboxed thread only takes back memories made in it", ErrMemoryScopeMove)
		}
		return memoryScopeTarget{scope: memory.ScopeChat, chatID: &dest.ID}, nil

	default:
		return memoryScopeTarget{}, fmt.Errorf("%w: invalid memory level: %s", ErrInvalidRequestBody, move.Level)
	}
}

// matches reports whether the memory is already where t would put it.
func (t memoryScopeTarget) matches(m *ent.Memory) bool {
	return m.Scope == t.scope && sameUUIDPtr(memoryChatID(m), t.chatID) && sameUUIDPtr(m.PinnedPersonalityID, t.pinned)
}

func sameUUIDPtr(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// memoryChatID is the memory's chat edge (its thread, or the thread it came from), when loaded.
func memoryChatID(m *ent.Memory) *uuid.UUID {
	if m.Edges.Chat == nil {
		return nil
	}
	id := m.Edges.Chat.ID
	return &id
}

// memoryChatPersonalityID is the personality of the memory's chat, when it and its personality are loaded.
func memoryChatPersonalityID(m *ent.Memory) *uuid.UUID {
	if m.Edges.Chat == nil || m.Edges.Chat.Edges.Personality == nil {
		return nil
	}
	id := m.Edges.Chat.Edges.Personality.ID
	return &id
}

// auditMemoryScopeMove records a scope move by ids and levels only, never the memory's content.
func (d *Datastore) auditMemoryScopeMove(ctx context.Context, userID uuid.UUID, before, after *ent.Memory) {
	from, to := memoryLevelForEntity(before), memoryLevelForEntity(after)
	meta := map[string]any{
		"memory_id":  before.ID.String(),
		"from_level": string(from),
		"to_level":   string(to),
	}
	if id := memoryChatID(before); id != nil {
		meta["from_chat_id"] = id.String()
	}
	if id := memoryChatID(after); id != nil {
		meta["to_chat_id"] = id.String()
	}
	if before.PinnedPersonalityID != nil {
		meta["from_personality_id"] = before.PinnedPersonalityID.String()
	}
	if after.PinnedPersonalityID != nil {
		meta["to_personality_id"] = after.PinnedPersonalityID.String()
	}
	subject := userID
	d.writeAuditLog(ctx, auditEntry{
		Category:      auditCategoryMemory,
		Action:        "scope_move",
		Message:       fmt.Sprintf("memory moved from %s to %s", from, to),
		SubjectUserID: &subject,
		Metadata:      meta,
	})
}
