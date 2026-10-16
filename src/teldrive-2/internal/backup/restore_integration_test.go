//go:build integration

package backup_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tgdrive/teldrive/v2/internal/backup"
	testpostgres "github.com/tgdrive/teldrive/v2/internal/testutil/postgres"
)

// These tests are deliberately part of the integration suite rather than the
// unit suite: a restore is almost entirely statement ordering, and the ordering
// only means anything against a real PostgreSQL with the real constraints. The
// self-referencing foreign key on files.parent_id and the ON DELETE RESTRICT it
// carries are exactly what a mock would get wrong.
//
// Run them with the project harness:
//
//	TEST_DATABASE_URL=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable \
//	    go test -tags=integration ./internal/backup/

const testUserID int64 = 42

func newRestorer(t *testing.T) (*backup.Restorer, *pgxpool.Pool) {
	t.Helper()
	database := testpostgres.New(t)
	if _, err := database.Pool.Exec(context.Background(),
		`INSERT INTO teldrive.users (user_id, display_name) VALUES ($1, 'restore test')`, testUserID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return backup.NewRestorer(database.Pool, nil), database.Pool
}

// seedFile inserts a catalog row directly, standing in for an upload.
func seedFile(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, parent uuid.UUID, name, kind string, size *int64) {
	t.Helper()
	var parentValue any
	if parent != uuid.Nil {
		parentValue = parent
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO teldrive.files (id, user_id, parent_id, name, kind, size, status, mod_time)
		 VALUES ($1, $2, $3, $4, $5::teldrive.file_kind, $6, 'active', now())`,
		id, testUserID, parentValue, name, kind, size); err != nil {
		t.Fatalf("seed file %s: %v", name, err)
	}
}

func seedPart(t *testing.T, pool *pgxpool.Pool, fileID uuid.UUID, partNo int32, channelID, messageID int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO teldrive.file_parts (file_id, part_no, channel_id, message_id, plain_size, stored_size)
		 VALUES ($1, $2, $3, $4, 100, 100)`,
		fileID, partNo, channelID, messageID); err != nil {
		t.Fatalf("seed part: %v", err)
	}
}

// countFiles and countParts are scoped to the test user. file_parts has no
// user_id of its own, so its count goes through the file it belongs to.
func countFiles(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var value int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM teldrive.files WHERE user_id = $1`, testUserID).Scan(&value); err != nil {
		t.Fatalf("count files: %v", err)
	}
	return value
}

func countParts(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var value int
	if err := pool.QueryRow(context.Background(), `
SELECT count(*) FROM teldrive.file_parts p
JOIN teldrive.files f ON f.id = p.file_id
WHERE f.user_id = $1`, testUserID).Scan(&value); err != nil {
		t.Fatalf("count parts: %v", err)
	}
	return value
}

func exists(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var found bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM teldrive.files WHERE id = $1)`, id).Scan(&found); err != nil {
		t.Fatalf("look up %s: %v", id, err)
	}
	return found
}

func size(v int64) *int64 { return &v }

// TestRestoreOverwriteReplacesTheCatalog covers the destructive path: the rows
// the snapshot describes come back, and everything else goes -- including trash
// and the parts that belonged to it.
func TestRestoreOverwriteReplacesTheCatalog(t *testing.T) {
	restorer, pool := newRestorer(t)
	ctx := context.Background()

	folder := uuid.New()
	file := uuid.New()
	stale := uuid.New()

	seedFile(t, pool, folder, uuid.Nil, "Photos", "folder", nil)
	seedFile(t, pool, file, folder, "cat.jpg", "file", size(2048))
	seedPart(t, pool, file, 1, 100, 5001)
	seedFile(t, pool, stale, uuid.Nil, "gone.txt", "file", size(10))
	seedPart(t, pool, stale, 1, 100, 5002)
	// A trashed row is not in the index and must not survive an overwrite.
	if _, err := pool.Exec(ctx,
		`INSERT INTO teldrive.files (id, user_id, name, kind, size, status, deleted_at, mod_time)
		 VALUES ($1, $2, 'trashed.bin', 'file', 5, 'trashed', now(), now())`,
		uuid.New(), testUserID); err != nil {
		t.Fatalf("seed trashed file: %v", err)
	}

	index := &backup.Index{
		Format:  backup.IndexFormat,
		Version: backup.IndexVersion,
		UserID:  testUserID,
		Channels: []backup.Channel{
			{ChannelID: 100, Name: "data", Selected: true, Health: "healthy"},
		},
		Files: []backup.File{
			{ID: folder.String(), Name: "Photos", Kind: "folder", Status: "active", Generation: 1},
			{ID: file.String(), ParentID: folder.String(), Name: "cat.jpg", Kind: "file", Status: "active", Size: size(2048), Generation: 1},
		},
		Parts: []backup.Part{
			{FileID: file.String(), PartNo: 1, ChannelID: 100, MessageID: 5001, PlainSize: size(2048), StoredSize: size(2048)},
		},
	}

	result, err := restorer.Apply(ctx, testUserID, index, backup.RestoreOverwrite)
	if err != nil {
		t.Fatalf("apply overwrite: %v", err)
	}

	if result.Files != 2 || result.Folders != 1 || result.Parts != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.DeletedFiles != 4 {
		t.Fatalf("deleted = %d, want 4 (the two seeded rows, the stale file and the trashed one)", result.DeletedFiles)
	}
	if !exists(t, pool, folder) || !exists(t, pool, file) {
		t.Fatal("the snapshot's own rows were not restored")
	}
	if exists(t, pool, stale) {
		t.Fatal("a file the snapshot does not mention survived an overwrite")
	}
	if got := countFiles(t, pool); got != 2 {
		t.Fatalf("files = %d, want 2", got)
	}
	if got := countParts(t, pool); got != 1 {
		t.Fatalf("file_parts = %d, want 1", got)
	}
	// The parts of the deleted file must be gone through the cascade, not
	// orphaned next to a vanished file.
	var orphanParts int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM teldrive.file_parts p
WHERE NOT EXISTS (SELECT 1 FROM teldrive.files f WHERE f.id = p.file_id)`).Scan(&orphanParts); err != nil {
		t.Fatalf("count orphan parts: %v", err)
	}
	if orphanParts != 0 {
		t.Fatalf("orphan parts = %d, want 0", orphanParts)
	}
}

// TestRestoreMergeOnlyAdds is the recovery story: get back what was lost without
// touching what is still there.
func TestRestoreMergeOnlyAdds(t *testing.T) {
	restorer, pool := newRestorer(t)
	ctx := context.Background()

	folder := uuid.New()
	kept := uuid.New()
	lost := uuid.New()

	seedFile(t, pool, folder, uuid.Nil, "Documents", "folder", nil)
	seedFile(t, pool, kept, folder, "keep.txt", "file", size(11))

	index := &backup.Index{
		Format:  backup.IndexFormat,
		Version: backup.IndexVersion,
		UserID:  testUserID,
		Files: []backup.File{
			{ID: folder.String(), Name: "Documents", Kind: "folder", Status: "active", Generation: 1},
			{ID: kept.String(), ParentID: folder.String(), Name: "keep.txt", Kind: "file", Status: "active", Size: size(11), Generation: 1},
			{ID: lost.String(), ParentID: folder.String(), Name: "lost.txt", Kind: "file", Status: "active", Size: size(12), Generation: 1},
		},
		Parts: []backup.Part{
			{FileID: lost.String(), PartNo: 1, ChannelID: 100, MessageID: 7001, PlainSize: size(12), StoredSize: size(12)},
		},
	}

	result, err := restorer.Apply(ctx, testUserID, index, backup.RestoreMerge)
	if err != nil {
		t.Fatalf("apply merge: %v", err)
	}
	if result.DeletedFiles != 0 {
		t.Fatalf("merge deleted %d rows; it must never delete", result.DeletedFiles)
	}
	if result.Files != 1 || result.SkippedFiles != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !exists(t, pool, lost) {
		t.Fatal("the missing file was not restored")
	}
	if !exists(t, pool, kept) {
		t.Fatal("merge removed a file that was already there")
	}
	if got := countFiles(t, pool); got != 3 {
		t.Fatalf("files = %d, want 3", got)
	}
}

// TestRestoreOrphansAreReported pins the case the foreign key would otherwise
// turn into a crash: a file whose parent exists in neither the catalog nor the
// index has nowhere to go, so it is reported instead of inserted.
func TestRestoreOrphansAreReported(t *testing.T) {
	restorer, pool := newRestorer(t)
	ctx := context.Background()

	sibling := uuid.New()
	orphan := uuid.New()
	// The database cannot hold a row like the orphan -- the foreign key forbids
	// it -- which is exactly why the orphan can only arrive in an index that was
	// hand-edited or assembled from a partly lost dump.
	missingParent := uuid.New()

	index := &backup.Index{
		Format:  backup.IndexFormat,
		Version: backup.IndexVersion,
		UserID:  testUserID,
		Files: []backup.File{
			{ID: sibling.String(), Name: "fine.txt", Kind: "file", Status: "active", Size: size(1), Generation: 1},
			{ID: orphan.String(), ParentID: missingParent.String(), Name: "nowhere.txt", Kind: "file", Status: "active", Size: size(1), Generation: 1},
		},
	}

	result, err := restorer.Apply(ctx, testUserID, index, backup.RestoreMerge)
	if err != nil {
		t.Fatalf("apply merge: %v", err)
	}
	if len(result.Orphans) != 1 || result.Orphans[0] != orphan.String() {
		t.Fatalf("orphans = %v, want [%s]", result.Orphans, orphan)
	}
	if !exists(t, pool, sibling) {
		t.Fatal("the reachable file was not restored alongside the orphan")
	}
	if exists(t, pool, orphan) {
		t.Fatal("a file with no parent must not be inserted")
	}
}

// TestRestoreRefusesWhileUploadsAreRunning checks the guard that keeps a restore
// from pulling a folder out from under an upload that is still writing to it.
func TestRestoreRefusesWhileUploadsAreRunning(t *testing.T) {
	restorer, pool := newRestorer(t)
	ctx := context.Background()

	folder := uuid.New()
	seedFile(t, pool, folder, uuid.Nil, "Incoming", "folder", nil)
	if _, err := pool.Exec(ctx, `
INSERT INTO teldrive.upload_sessions (id, user_id, parent_id, name, expected_size, mod_time, part_size, state, expires_at)
VALUES ($1, $2, $3, 'big.bin', 100, now(), 100, 'open', now() + interval '1 hour')`,
		uuid.New(), testUserID, folder); err != nil {
		t.Fatalf("seed upload session: %v", err)
	}

	index := &backup.Index{
		Format:  backup.IndexFormat,
		Version: backup.IndexVersion,
		UserID:  testUserID,
		Files:   []backup.File{{ID: folder.String(), Name: "Incoming", Kind: "folder", Status: "active", Generation: 1}},
	}

	_, err := restorer.Apply(ctx, testUserID, index, backup.RestoreOverwrite)
	if !errors.Is(err, backup.ErrUploadsInProgress) {
		t.Fatalf("err = %v, want ErrUploadsInProgress", err)
	}
	if !exists(t, pool, folder) {
		t.Fatal("a refused restore must not have deleted anything")
	}
}

// TestRestoreRollsBackOnFailure is the reason Apply uses one transaction: a cycle
// the delete loop cannot break must leave the catalog exactly as it found it,
// not half-emptied.
func TestRestoreRollsBackOnFailure(t *testing.T) {
	restorer, pool := newRestorer(t)
	ctx := context.Background()

	first := uuid.New()
	second := uuid.New()
	seedFile(t, pool, first, uuid.Nil, "a", "folder", nil)
	seedFile(t, pool, second, first, "b", "folder", nil)
	// Close the loop. The foreign key is satisfied because both rows exist, so
	// the only thing standing between here and an infinite delete is the guard.
	if _, err := pool.Exec(ctx, `UPDATE teldrive.files SET parent_id = $1 WHERE id = $2`, second, first); err != nil {
		t.Fatalf("create cycle: %v", err)
	}

	index := &backup.Index{
		Format:  backup.IndexFormat,
		Version: backup.IndexVersion,
		UserID:  testUserID,
		Files:   []backup.File{{ID: first.String(), Name: "a", Kind: "folder", Status: "active", Generation: 1}},
	}

	_, err := restorer.Apply(ctx, testUserID, index, backup.RestoreOverwrite)
	if !errors.Is(err, backup.ErrCatalogCycle) {
		t.Fatalf("err = %v, want ErrCatalogCycle", err)
	}
	if got := countFiles(t, pool); got != 2 {
		t.Fatalf("files = %d, want 2: the failed restore was not rolled back", got)
	}
}

// TestRestoreMergeReaddsTheParentChain covers the shape a real recovery has: a
// whole subtree is gone, and the index still holds the chain that connects it to
// the root, so the wave insertion has to walk down it in order.
func TestRestoreMergeReaddsTheParentChain(t *testing.T) {
	restorer, pool := newRestorer(t)
	ctx := context.Background()

	root := uuid.New()
	middle := uuid.New()
	leaf := uuid.New()

	index := &backup.Index{
		Format:  backup.IndexFormat,
		Version: backup.IndexVersion,
		UserID:  testUserID,
		Files: []backup.File{
			// Deliberately out of order: the leaf is listed before its parent,
			// which is exactly what a JSON dump of a recursive query looks like.
			{ID: leaf.String(), ParentID: middle.String(), Name: "deep.txt", Kind: "file", Status: "active", Size: size(3), Generation: 1},
			{ID: root.String(), Name: "root", Kind: "folder", Status: "active", Generation: 1},
			{ID: middle.String(), ParentID: root.String(), Name: "middle", Kind: "folder", Status: "active", Generation: 1},
		},
	}

	result, err := restorer.Apply(ctx, testUserID, index, backup.RestoreMerge)
	if err != nil {
		t.Fatalf("apply merge: %v", err)
	}
	if result.Files != 3 || result.Folders != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Orphans) != 0 {
		t.Fatalf("orphans = %v, want none: the chain is complete", result.Orphans)
	}
	if !exists(t, pool, leaf) {
		t.Fatal("the deepest file was not restored")
	}
}

// TestRestoreRejectsATamperedSnapshot is the security boundary: the document
// arrives as a file in Telegram, so a modified one has to be refused before
// anything is touched.
func TestRestoreRejectsATamperedSnapshot(t *testing.T) {
	restorer, pool := newRestorer(t)
	ctx := context.Background()

	folder := uuid.New()
	seedFile(t, pool, folder, uuid.Nil, "keep", "folder", nil)

	cases := map[string]*backup.Index{
		"another user": {
			Format: backup.IndexFormat, Version: backup.IndexVersion, UserID: testUserID + 1,
		},
		"unknown format": {
			Format: "something-else", Version: backup.IndexVersion, UserID: testUserID,
		},
		"newer version": {
			Format: backup.IndexFormat, Version: backup.IndexVersion + 1, UserID: testUserID,
		},
		"blank name": {
			Format: backup.IndexFormat, Version: backup.IndexVersion, UserID: testUserID,
			Files: []backup.File{{ID: uuid.NewString(), Name: "  ", Kind: "folder", Status: "active"}},
		},
		"folder with a size": {
			Format: backup.IndexFormat, Version: backup.IndexVersion, UserID: testUserID,
			Files: []backup.File{{ID: uuid.NewString(), Name: "x", Kind: "folder", Status: "active", Size: size(1)}},
		},
		"part for an absent file": {
			Format: backup.IndexFormat, Version: backup.IndexVersion, UserID: testUserID,
			Parts: []backup.Part{{FileID: uuid.NewString(), PartNo: 1, ChannelID: 1, MessageID: 2}},
		},
		"self parent": {
			Format: backup.IndexFormat, Version: backup.IndexVersion, UserID: testUserID,
			Files: []backup.File{{ID: folder.String(), ParentID: folder.String(), Name: "loop", Kind: "folder", Status: "active"}},
		},
	}

	for name, index := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := restorer.Apply(ctx, testUserID, index, backup.RestoreOverwrite); !errors.Is(err, backup.ErrInvalidIndex) {
				t.Fatalf("err = %v, want ErrInvalidIndex", err)
			}
		})
	}
	if !exists(t, pool, folder) {
		t.Fatal("a rejected restore must not have deleted anything")
	}
}

// TestRestoreAddsMissingAccountsWithoutChangingRoles covers rebuilding on a
// fresh database, where the catalog references accounts that are not there yet.
//
// The second half is the safety property: an account that already exists must
// keep its role. A restore that could re-grant administrator rights would turn
// "read a chat message" into a privilege escalation.
func TestRestoreAddsMissingAccountsWithoutChangingRoles(t *testing.T) {
	restorer, pool := newRestorer(t)
	ctx := context.Background()

	// The harness seeds the test user with the default role.
	var roleBefore string
	if err := pool.QueryRow(ctx, `SELECT role::text FROM teldrive.users WHERE user_id = $1`, testUserID).Scan(&roleBefore); err != nil {
		t.Fatalf("read seeded role: %v", err)
	}

	folder := uuid.New()
	index := &backup.Index{
		Format:  backup.IndexFormat,
		Version: backup.IndexVersion,
		UserID:  testUserID,
		Content: backup.Content{Users: true, EncryptionKeys: true},
		Users: []backup.User{
			// An existing account, claimed to be the owner by the snapshot.
			{UserID: testUserID, DisplayName: "Renamed By Restore", Username: "restored", Role: "owner"},
			// An account that is genuinely missing, which the catalog needs.
			{UserID: testUserID + 1, DisplayName: "Recovered", Username: "recovered", Role: "user"},
		},
		Secrets: &backup.Secrets{EncryptionKeys: map[string]string{"1": "key-material"}},
		Files: []backup.File{
			{ID: folder.String(), Name: "Docs", Kind: "folder", Status: "active", Generation: 1},
		},
	}

	result, err := restorer.Apply(ctx, testUserID, index, backup.RestoreMerge)
	if err != nil {
		t.Fatalf("apply merge: %v", err)
	}
	if result.Users != 2 {
		t.Fatalf("users = %d, want 2", result.Users)
	}
	if !result.SecretsIncluded {
		t.Fatal("the result should report that the snapshot carried keys")
	}

	var restoredName, restoredRole string
	if err := pool.QueryRow(ctx, `
SELECT display_name, role::text FROM teldrive.users WHERE user_id = $1`, testUserID+1).Scan(&restoredName, &restoredRole); err != nil {
		t.Fatalf("the missing account was not created: %v", err)
	}
	if restoredName != "Recovered" || restoredRole != "user" {
		t.Fatalf("restored account = %q/%q", restoredName, restoredRole)
	}

	var roleAfter, nameAfter string
	if err := pool.QueryRow(ctx, `
SELECT role::text, display_name FROM teldrive.users WHERE user_id = $1`, testUserID).Scan(&roleAfter, &nameAfter); err != nil {
		t.Fatalf("read the existing account: %v", err)
	}
	if roleAfter != roleBefore {
		t.Fatalf("role changed from %q to %q: a restore must not hand out administrator rights", roleBefore, roleAfter)
	}
	if nameAfter != "Renamed By Restore" {
		t.Fatalf("display name = %q, want the cosmetic fields updated", nameAfter)
	}
}
