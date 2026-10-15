package datastore

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/ent/auditlog"
	entchat "github.com/theimaginaryfoundation/what-iff/ent/chat"
	entmemory "github.com/theimaginaryfoundation/what-iff/ent/memory"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// createAuditLogTestTable adds the audit_logs table MoveMemoryScope writes to.
func createAuditLogTestTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE audit_logs (
		id uuid PRIMARY KEY,
		occurred_at datetime NOT NULL,
		category text NOT NULL,
		action text NOT NULL,
		message text NOT NULL,
		actor_user_id uuid,
		subject_user_id uuid
	)`)
	require.NoError(t, err)
}

type scopeMoveFixture struct {
	ds          *Datastore
	userID      uuid.UUID
	personaA    uuid.UUID
	personaB    uuid.UUID
	threadA     uuid.UUID // personality A
	otherA      uuid.UUID // personality A
	threadB     uuid.UUID // personality B
	sandboxedA  uuid.UUID // personality A, sandboxed
	unassigned  uuid.UUID // no personality
	otherUserID uuid.UUID
}

func newScopeMoveFixture(t *testing.T) *scopeMoveFixture {
	t.Helper()
	ds, cleanup := newTestDatastore(t, createMemoryImportTestSchema, alterChatsTableForAgentJobTests, createAuditLogTestTable)
	t.Cleanup(cleanup)
	ctx := context.Background()

	f := &scopeMoveFixture{ds: ds, userID: uuid.New(), otherUserID: uuid.New()}
	createTestUser(t, ds, f.userID)
	createTestUser(t, ds, f.otherUserID)
	f.personaA, f.personaB = uuid.New(), uuid.New()
	createTestPersonality(t, ds, f.personaA, f.userID)
	createTestPersonality(t, ds, f.personaB, f.userID)

	chat := func(personalityID uuid.UUID) uuid.UUID {
		id := uuid.New()
		createTestChat(t, ds, id, f.userID)
		if personalityID != uuid.Nil {
			require.NoError(t, ds.dbClient.Chat.UpdateOneID(id).SetPersonalityID(personalityID).Exec(ctx))
		}
		return id
	}
	f.threadA, f.otherA, f.threadB = chat(f.personaA), chat(f.personaA), chat(f.personaB)
	f.sandboxedA = chat(f.personaA)
	require.NoError(t, ds.dbClient.Chat.UpdateOneID(f.sandboxedA).SetContextScope(entchat.ContextScopeSandbox).Exec(ctx))
	f.unassigned = chat(uuid.Nil)
	return f
}

func (f *scopeMoveFixture) threadMemory(t *testing.T, chatID uuid.UUID) *models.Memory {
	t.Helper()
	mem, err := f.ds.CreateMemoryFromInput(context.Background(), f.userID, models.CreateMemoryInput{
		Content: "likes rain", Level: models.MemoryLevelThread, ChatID: &chatID,
	})
	require.NoError(t, err)
	return mem
}

func (f *scopeMoveFixture) move(t *testing.T, id uuid.UUID, move models.MemoryScopeMove) (*models.Memory, error) {
	t.Helper()
	return f.ds.MoveMemoryScope(context.Background(), f.userID, id, move)
}

func (f *scopeMoveFixture) auditRows(t *testing.T) []string {
	t.Helper()
	rows, err := f.ds.dbClient.AuditLog.Query().Where(auditlog.Category(auditCategoryMemory)).All(context.Background())
	require.NoError(t, err)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		require.Equal(t, "scope_move", r.Action)
		require.Equal(t, f.userID, *r.SubjectUserID)
		out = append(out, r.Message)
	}
	return out
}

func TestMoveMemoryScope_PromoteThreadMemoryMovesTheRecordAndKeepsItsOrigin(t *testing.T) {
	f := newScopeMoveFixture(t)
	ctx := context.Background()
	mem := f.threadMemory(t, f.threadA)
	seedEmbedding(t, f.ds, mem.ID, []float32{1, 2})

	global, err := f.move(t, mem.ID, models.MemoryScopeMove{Level: models.MemoryLevelGlobal})
	require.NoError(t, err)
	require.Equal(t, mem.ID, global.ID, "the record moves, it is not copied")
	require.Equal(t, models.MemoryLevelGlobal, global.Level)
	require.Equal(t, f.threadA, global.ChatID, "the source thread is kept as provenance")
	require.Nil(t, global.PinnedPersonalityID)
	require.Equal(t, "likes rain", global.Content)

	count, err := f.ds.dbClient.Memory.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count, "a move creates no duplicate")
	require.Len(t, embeddingRowsFor(t, f.ds, mem.ID), 1, "content is unchanged, so the embedding is kept")

	audit := f.auditRows(t)
	require.Len(t, audit, 1)
	require.Contains(t, audit[0], "memory moved from thread to global")
	require.Contains(t, audit[0], f.threadA.String())
	require.NotContains(t, audit[0], "likes rain", "the audit log never holds memory content")
}

func TestMoveMemoryScope_PromoteToPersonality(t *testing.T) {
	f := newScopeMoveFixture(t)

	pinned, err := f.move(t, f.threadMemory(t, f.threadA).ID, models.MemoryScopeMove{Level: models.MemoryLevelPersonality})
	require.NoError(t, err)
	require.Equal(t, models.MemoryLevelPersonality, pinned.Level)
	require.Equal(t, f.personaA, *pinned.PinnedPersonalityID)
	require.Equal(t, f.threadA, pinned.ChatID)

	crossed, err := f.move(t, f.threadMemory(t, f.threadA).ID, models.MemoryScopeMove{Level: models.MemoryLevelPersonality, PersonalityID: &f.personaB})
	require.NoError(t, err, "the user may pin a thread memory to any of their personalities")
	require.Equal(t, f.personaB, *crossed.PinnedPersonalityID)
	require.Equal(t, f.threadA, crossed.ChatID, "it still remembers the thread it came from")

	// A thread with no personality has none to default to: the caller must name one.
	loose := f.threadMemory(t, f.unassigned)
	_, err = f.move(t, loose.ID, models.MemoryScopeMove{Level: models.MemoryLevelPersonality})
	require.ErrorIs(t, err, ErrMemoryScopeMove)
	pinnedLoose, err := f.move(t, loose.ID, models.MemoryScopeMove{Level: models.MemoryLevelPersonality, PersonalityID: &f.personaB})
	require.NoError(t, err)
	require.Equal(t, f.personaB, *pinnedLoose.PinnedPersonalityID)
}

func TestMoveMemoryScope_BackToThread(t *testing.T) {
	f := newScopeMoveFixture(t)
	promoted := func(level models.MemoryLevel) uuid.UUID {
		mem := f.threadMemory(t, f.threadA)
		_, err := f.move(t, mem.ID, models.MemoryScopeMove{Level: level})
		require.NoError(t, err)
		return mem.ID
	}

	t.Run("returns to its origin thread", func(t *testing.T) {
		id := promoted(models.MemoryLevelPersonality)
		back, err := f.move(t, id, models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &f.threadA})
		require.NoError(t, err)
		require.Equal(t, models.MemoryLevelThread, back.Level)
		require.Equal(t, f.threadA, back.ChatID)
		require.Nil(t, back.PinnedPersonalityID)
	})

	t.Run("may go to another thread of the same personality", func(t *testing.T) {
		back, err := f.move(t, promoted(models.MemoryLevelGlobal), models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &f.otherA})
		require.NoError(t, err)
		require.Equal(t, f.otherA, back.ChatID)
	})

	t.Run("to a thread of another personality", func(t *testing.T) {
		for _, level := range []models.MemoryLevel{models.MemoryLevelGlobal, models.MemoryLevelPersonality} {
			back, err := f.move(t, promoted(level), models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &f.threadB})
			require.NoError(t, err, level)
			require.Equal(t, f.threadB, back.ChatID, level)
			require.Nil(t, back.PinnedPersonalityID, "a thread memory carries no pin")
		}
	})

	t.Run("from one thread to another", func(t *testing.T) {
		mem := f.threadMemory(t, f.threadA)
		moved, err := f.move(t, mem.ID, models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &f.threadB})
		require.NoError(t, err)
		require.Equal(t, models.MemoryLevelThread, moved.Level)
		require.Equal(t, f.threadB, moved.ChatID)

		same, err := f.move(t, mem.ID, models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &f.threadB})
		require.NoError(t, err, "a move to its own thread is a no-op")
		require.Equal(t, moved.UpdatedAt, same.UpdatedAt)
	})

	t.Run("the destination is never defaulted", func(t *testing.T) {
		_, err := f.move(t, promoted(models.MemoryLevelGlobal), models.MemoryScopeMove{Level: models.MemoryLevelThread})
		require.ErrorIs(t, err, ErrMemoryScopeMove)
	})

	t.Run("a sandboxed thread takes back only its own memories", func(t *testing.T) {
		_, err := f.move(t, promoted(models.MemoryLevelGlobal), models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &f.sandboxedA})
		require.ErrorIs(t, err, ErrMemoryScopeMove)

		own := f.threadMemory(t, f.sandboxedA)
		_, err = f.move(t, own.ID, models.MemoryScopeMove{Level: models.MemoryLevelGlobal})
		require.NoError(t, err)
		back, err := f.move(t, own.ID, models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &f.sandboxedA})
		require.NoError(t, err)
		require.Equal(t, f.sandboxedA, back.ChatID)
	})

	t.Run("a memory with no origin goes to the thread the caller picks, whatever its pin", func(t *testing.T) {
		global, err := f.ds.CreateMemoryFromInput(context.Background(), f.userID, models.CreateMemoryInput{Content: "no origin", Level: models.MemoryLevelGlobal})
		require.NoError(t, err)
		back, err := f.move(t, global.ID, models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &f.threadB})
		require.NoError(t, err)
		require.Equal(t, f.threadB, back.ChatID)

		pinned, err := f.ds.CreateMemoryFromInput(context.Background(), f.userID, models.CreateMemoryInput{
			Content: "pinned, no origin", Level: models.MemoryLevelPersonality, PinnedPersonalityID: &f.personaB,
		})
		require.NoError(t, err)
		moved, err := f.move(t, pinned.ID, models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &f.threadA})
		require.NoError(t, err)
		require.Equal(t, f.threadA, moved.ChatID)
	})
}

func TestMoveMemoryScope_EnforcesOwnership(t *testing.T) {
	f := newScopeMoveFixture(t)
	ctx := context.Background()
	mem := f.threadMemory(t, f.threadA)

	_, err := f.ds.MoveMemoryScope(ctx, f.otherUserID, mem.ID, models.MemoryScopeMove{Level: models.MemoryLevelGlobal})
	require.ErrorIs(t, err, ErrMemoryNotFound, "another user's memory is not found")

	foreignChat := uuid.New()
	createTestChat(t, f.ds, foreignChat, f.otherUserID)
	foreignPersonality := uuid.New()
	createTestPersonality(t, f.ds, foreignPersonality, f.otherUserID)

	global, err := f.ds.CreateMemoryFromInput(ctx, f.userID, models.CreateMemoryInput{Content: "mine", Level: models.MemoryLevelGlobal})
	require.NoError(t, err)
	_, err = f.move(t, global.ID, models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &foreignChat})
	require.ErrorIs(t, err, ErrChatNotFound)
	_, err = f.move(t, global.ID, models.MemoryScopeMove{Level: models.MemoryLevelPersonality, PersonalityID: &foreignPersonality})
	require.ErrorIs(t, err, ErrPersonalityNotFound)

	reloaded, err := f.ds.GetMemory(ctx, f.userID, global.ID)
	require.NoError(t, err)
	require.Equal(t, models.MemoryLevelGlobal, reloaded.Level, "a refused move changes nothing")
	require.Empty(t, f.auditRows(t))
}

func TestMoveMemoryScope_RejectsSummariesAndMalformedMoves(t *testing.T) {
	f := newScopeMoveFixture(t)
	ctx := context.Background()

	summary, err := f.ds.dbClient.Memory.Create().
		SetContent("checkpoint").SetScope(entmemory.ScopeSummary).SetOwnerID(f.userID).SetChatID(f.threadA).
		Save(ctx)
	require.NoError(t, err)
	_, err = f.move(t, summary.ID, models.MemoryScopeMove{Level: models.MemoryLevelGlobal})
	require.ErrorIs(t, err, ErrMemoryScopeMove)

	mem := f.threadMemory(t, f.threadA)
	for name, move := range map[string]models.MemoryScopeMove{
		"unknown level":           {Level: "everywhere"},
		"summary level":           {Level: models.MemoryLevelSummary},
		"global with a chat":      {Level: models.MemoryLevelGlobal, ChatID: &f.otherA},
		"personality with a chat": {Level: models.MemoryLevelPersonality, ChatID: &f.otherA},
	} {
		_, err := f.move(t, mem.ID, move)
		require.ErrorIs(t, err, ErrInvalidRequestBody, name)
	}
	_, err = f.move(t, mem.ID, models.MemoryScopeMove{Level: models.MemoryLevelThread})
	require.ErrorIs(t, err, ErrMemoryScopeMove, "the destination thread is never defaulted")
}

func TestMoveMemoryScope_NoOpIsNotAudited(t *testing.T) {
	f := newScopeMoveFixture(t)
	mem := f.threadMemory(t, f.threadA)
	_, err := f.move(t, mem.ID, models.MemoryScopeMove{Level: models.MemoryLevelGlobal})
	require.NoError(t, err)
	again, err := f.move(t, mem.ID, models.MemoryScopeMove{Level: models.MemoryLevelGlobal})
	require.NoError(t, err)
	require.Equal(t, models.MemoryLevelGlobal, again.Level)
	require.Len(t, f.auditRows(t), 1)
}

// GetRelatedMemories ranks with pgvector SQL sqlite cannot run, so this checks the scope filter it
// retrieves through: after each move the memory is readable exactly where its new scope says.
func TestMoveMemoryScope_RetrievalFollowsTheNewScope(t *testing.T) {
	f := newScopeMoveFixture(t)
	ctx := context.Background()
	mem := f.threadMemory(t, f.threadA)

	readable := func(chatID, personalityID uuid.UUID, sandboxed bool) bool {
		ok, err := f.ds.dbClient.Memory.Query().
			Where(entmemory.ID(mem.ID), relatedMemoryReadable(chatID, personalityID, sandboxed)).
			Exist(ctx)
		require.NoError(t, err)
		return ok
	}

	require.True(t, readable(f.threadA, f.personaA, false))
	require.False(t, readable(f.otherA, f.personaA, false), "a thread memory is read only in its thread")

	_, err := f.move(t, mem.ID, models.MemoryScopeMove{Level: models.MemoryLevelGlobal})
	require.NoError(t, err)
	require.True(t, readable(f.otherA, f.personaA, false))
	require.True(t, readable(f.threadB, f.personaB, false), "global is read with every personality")
	require.False(t, readable(f.threadA, f.personaA, true), "a sandbox reads no account memory, its origin included")

	_, err = f.move(t, mem.ID, models.MemoryScopeMove{Level: models.MemoryLevelPersonality, PersonalityID: &f.personaA})
	require.NoError(t, err)
	require.True(t, readable(f.otherA, f.personaA, false))
	require.False(t, readable(f.threadB, f.personaB, false), "a personality memory is read only with that personality")

	_, err = f.move(t, mem.ID, models.MemoryScopeMove{Level: models.MemoryLevelThread, ChatID: &f.otherA})
	require.NoError(t, err)
	require.True(t, readable(f.otherA, f.personaA, false))
	require.False(t, readable(f.threadA, f.personaA, false), "back in a thread, it is read only there")
}
