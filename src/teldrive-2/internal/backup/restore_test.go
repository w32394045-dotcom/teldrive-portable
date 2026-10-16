package backup

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseRestoreMode(t *testing.T) {
	cases := map[string]struct {
		input string
		want  RestoreMode
		fail  bool
	}{
		"empty is the default":  {"", RestoreOverwrite, false},
		"padded and shouted":    {"  OVERWRITE ", RestoreOverwrite, false},
		"merge":                 {"merge", RestoreMerge, false},
		"unknown is rejected":   {"replace", "", true},
		"a near miss is worse":  {"overwrit", "", true},
		"empty-ish is rejected": {"   ", RestoreOverwrite, false},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseRestoreMode(testCase.input)
			if testCase.fail {
				if err == nil {
					t.Fatalf("ParseRestoreMode(%q) = %q, want an error", testCase.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRestoreMode(%q): %v", testCase.input, err)
			}
			if got != testCase.want {
				t.Fatalf("ParseRestoreMode(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

// goodIndex is the smallest index that must be accepted; every rejection test
// below is a single mutation of it, so a failure points at exactly one rule.
func goodIndex() *Index {
	folder := uuid.NewString()
	file := uuid.NewString()
	size := int64(10)
	return &Index{
		Format:    IndexFormat,
		Version:   IndexVersion,
		CreatedAt: time.Now().UTC(),
		UserID:    7,
		Channels:  []Channel{{ChannelID: 100, Name: "data", Health: "healthy"}},
		Files: []File{
			{ID: folder, Name: "docs", Kind: "folder", Status: "active", Generation: 1},
			{ID: file, ParentID: folder, Name: "a.txt", Kind: "file", Status: "active", Size: &size, Generation: 1},
		},
		Parts: []Part{{FileID: file, PartNo: 1, ChannelID: 100, MessageID: 5, PlainSize: &size, StoredSize: &size}},
	}
}

func TestValidateAcceptsAWellFormedIndex(t *testing.T) {
	if err := goodIndex().Validate(7); err != nil {
		t.Fatalf("Validate on a good index: %v", err)
	}
}

// TestValidateRejectsBrokenIndexes is the gate that stands between a file in
// Telegram and the catalog. It is deliberately exhaustive: every one of these
// would otherwise fail halfway through a transaction, or worse, succeed and
// leave a drive that cannot be read.
func TestValidateRejectsBrokenIndexes(t *testing.T) {
	otherUser := int64(8)
	cases := map[string]func(*Index){
		"nil for the wrong user":   func(index *Index) { index.UserID = otherUser },
		"foreign format":           func(index *Index) { index.Format = "backup" },
		"version zero":             func(index *Index) { index.Version = 0 },
		"version from the future":  func(index *Index) { index.Version = IndexVersion + 1 },
		"id is not a uuid":         func(index *Index) { index.Files[0].ID = "not-a-uuid" },
		"duplicate id":             func(index *Index) { index.Files[1].ID = index.Files[0].ID },
		"blank name":               func(index *Index) { index.Files[0].Name = "   " },
		"unknown kind":             func(index *Index) { index.Files[0].Kind = "link" },
		"folder with a size":       func(index *Index) { size := int64(1); index.Files[0].Size = &size },
		"file without a size":      func(index *Index) { index.Files[1].Size = nil },
		"negative size":            func(index *Index) { size := int64(-1); index.Files[1].Size = &size },
		"parent is not a uuid":     func(index *Index) { index.Files[1].ParentID = "nope" },
		"file is its own parent":   func(index *Index) { index.Files[0].ParentID = index.Files[0].ID },
		"encrypted without a key":  func(index *Index) { index.Files[1].Encryption = true },
		"key without encryption":   func(index *Index) { version := int32(2); index.Files[1].EncryptionKeyVersion = &version },
		"half a hash":              func(index *Index) { index.Files[1].HashAlgorithm = "blake3" },
		"part for an absent file":  func(index *Index) { index.Parts[0].FileID = uuid.NewString() },
		"part number zero":         func(index *Index) { index.Parts[0].PartNo = 0 },
		"part without a channel":   func(index *Index) { index.Parts[0].ChannelID = 0 },
		"part without a message":   func(index *Index) { index.Parts[0].MessageID = 0 },
		"part with a bad size":     func(index *Index) { size := int64(-1); index.Parts[0].PlainSize = &size },
		"part with a bad stored s": func(index *Index) { size := int64(-1); index.Parts[0].StoredSize = &size },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			index := goodIndex()
			mutate(index)
			err := index.Validate(7)
			if !errors.Is(err, ErrInvalidIndex) {
				t.Fatalf("Validate() = %v, want ErrInvalidIndex", err)
			}
			if !strings.Contains(err.Error(), ErrInvalidIndex.Error()) {
				t.Fatalf("error %q should name the sentinel so the API can map it", err)
			}
		})
	}
}

func TestValidateRejectsAnEmptyIndex(t *testing.T) {
	var index *Index
	if err := index.Validate(7); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("Validate(nil) = %v, want ErrInvalidIndex", err)
	}
}

// TestValidateCountsCapsTheIndex keeps a hostile document from being parsed into
// gigabytes of rows before anything else rejects it.
func TestValidateCountsCapsTheIndex(t *testing.T) {
	index := goodIndex()
	index.Files = make([]File, maxRestoreFiles+1)
	if err := index.Validate(7); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("Validate(too many files) = %v, want ErrInvalidIndex", err)
	}
	index = goodIndex()
	index.Parts = make([]Part, maxRestoreParts+1)
	if err := index.Validate(7); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("Validate(too many parts) = %v, want ErrInvalidIndex", err)
	}
}

func TestStorePersistsTheRestoreMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.json")
	store := NewStore(path, nil)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// A fresh store has never been told what to do, and the honest default is
	// the mode that actually reproduces a snapshot.
	if got := store.Settings().RestoreMode; got != string(RestoreOverwrite) {
		t.Fatalf("default restore mode = %q, want %q", got, RestoreOverwrite)
	}
	if err := store.SetRestoreMode(RestoreMerge); err != nil {
		t.Fatalf("SetRestoreMode: %v", err)
	}
	if got := store.RestoreMode(); got != RestoreMerge {
		t.Fatalf("RestoreMode() = %q, want %q", got, RestoreMerge)
	}
	// The choice has to survive a restart, or the button would silently revert.
	reloaded := NewStore(path, nil)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.RestoreMode(); got != RestoreMerge {
		t.Fatalf("reloaded RestoreMode() = %q, want %q", got, RestoreMerge)
	}
}

func TestStoreRejectsAnUnknownRestoreMode(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "backup.json"), nil)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.SetRestoreMode(RestoreMode("destroy")); err == nil {
		t.Fatal("SetRestoreMode accepted an unknown mode")
	}
	if got := store.RestoreMode(); got != RestoreOverwrite {
		t.Fatalf("a rejected mode changed the setting to %q", got)
	}
}

// TestStoreRecoversFromAnUnreadableRestoreMode covers a hand-edited file: the
// settings still load, and the mode falls back rather than leaving a restore
// holding a value nothing understands.
func TestStoreRecoversFromAnUnreadableRestoreMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.json")
	if err := writeConfigFile(path, configFile{Enabled: true, RestoreMode: "overwrit"}); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store := NewStore(path, nil)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := store.RestoreMode(); got != RestoreOverwrite {
		t.Fatalf("RestoreMode() = %q, want %q", got, RestoreOverwrite)
	}
	if !store.Settings().Enabled {
		t.Fatal("the rest of the settings should still load")
	}
}

// TestRestoreModesAreOfferedInOrder pins the list the API publishes, so the UI
// cannot end up offering a value the parser rejects.
func TestRestoreModesAreOfferedInOrder(t *testing.T) {
	if len(RestoreModes) != 2 {
		t.Fatalf("RestoreModes = %v, want two entries", RestoreModes)
	}
	for _, mode := range RestoreModes {
		if !mode.Valid() {
			t.Fatalf("RestoreModes offers %q, which Valid() rejects", mode)
		}
	}
}
