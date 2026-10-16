package backup

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tgdrive/teldrive/v2/internal/db/sqlcgen"
)

func accountRow(t *testing.T, id int64, role sqlcgen.UserRole, disabled bool) *sqlcgen.User {
	t.Helper()
	row := &sqlcgen.User{
		UserID:      id,
		DisplayName: pgtype.Text{String: "User " + string(rune('A'+id%26)), Valid: true},
		Username:    pgtype.Text{String: "user" + string(rune('0'+id%10)), Valid: true},
		Role:        role,
	}
	if disabled {
		row.DisabledAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	return row
}

func buildWith(t *testing.T, queries *fakeQuerier, options BuildOptions) *Index {
	t.Helper()
	index, err := BuildIndexWith(context.Background(), queries, 7, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), options)
	if err != nil {
		t.Fatalf("BuildIndexWith: %v", err)
	}
	return index
}

// TestBuildIndexOnlyCarriesWhatWasSelected is the contract the settings page
// promises: a section that is switched off must not appear in the file at all.
func TestBuildIndexOnlyCarriesWhatWasSelected(t *testing.T) {
	queries := &fakeQuerier{users: []*sqlcgen.User{accountRow(t, 7, sqlcgen.UserRoleOwner, false)}}
	queries.files = append(queries.files, testFile(t, "a.txt", sqlcgen.FileKindFile))
	secrets := &Secrets{
		ActiveKeyVersion: func() *int32 { version := int32(1); return &version }(),
		EncryptionKeys:   map[string]string{"1": "super-secret"},
	}

	t.Run("nothing selected", func(t *testing.T) {
		index := buildWith(t, queries, BuildOptions{Secrets: secrets})
		if index.Users != nil || index.Secrets != nil {
			t.Fatalf("an unselected section was written: users=%v secrets=%v", index.Users, index.Secrets)
		}
		if index.Content.Any() {
			t.Fatalf("content = %+v, want nothing selected", index.Content)
		}
		if queries.userCalls != 0 {
			t.Fatalf("ListUsers was called %d times for a snapshot that does not carry accounts", queries.userCalls)
		}
		encoded, err := json.Marshal(index)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if strings.Contains(string(encoded), "super-secret") {
			t.Fatalf("the keys leaked into a snapshot that did not ask for them: %s", encoded)
		}
	})

	t.Run("everything selected", func(t *testing.T) {
		before := queries.userCalls
		index := buildWith(t, queries, BuildOptions{
			Content: Content{EncryptionKeys: true, Users: true},
			Secrets: secrets,
		})
		if len(index.Users) != 1 || index.Users[0].UserID != 7 {
			t.Fatalf("users = %+v", index.Users)
		}
		if index.Users[0].Role != string(sqlcgen.UserRoleOwner) {
			t.Fatalf("role = %q, want the stored role", index.Users[0].Role)
		}
		if index.Secrets == nil || index.Secrets.EncryptionKeys["1"] != "super-secret" {
			t.Fatalf("secrets = %+v", index.Secrets)
		}
		if index.Secrets.ActiveKeyVersion == nil || *index.Secrets.ActiveKeyVersion != 1 {
			t.Fatalf("active key version = %v", index.Secrets.ActiveKeyVersion)
		}
		if queries.userCalls != before+1 {
			t.Fatalf("ListUsers calls = %d, want one more", queries.userCalls)
		}
	})
}

// TestBuildIndexRecordsASelectionWithNothingToWrite covers the honest-reporting
// case: "the keys were requested but this instance has none" has to be
// distinguishable from "the keys were not requested".
func TestBuildIndexRecordsASelectionWithNothingToWrite(t *testing.T) {
	queries := &fakeQuerier{}
	index := buildWith(t, queries, BuildOptions{Content: Content{EncryptionKeys: true}})

	if !index.Content.EncryptionKeys {
		t.Fatal("the selection was not recorded")
	}
	if index.Secrets != nil {
		t.Fatalf("an empty secrets section was written: %+v", index.Secrets)
	}
	if summary := index.Summary(); summary.SecretsIncluded {
		t.Fatal("the summary claims keys that are not there")
	}
}

func TestSummaryReportsWhatIsInside(t *testing.T) {
	queries := &fakeQuerier{users: []*sqlcgen.User{
		accountRow(t, 7, sqlcgen.UserRoleOwner, false),
		accountRow(t, 8, sqlcgen.UserRoleUser, true),
	}}
	version := int32(2)
	index := buildWith(t, queries, BuildOptions{
		Content: Content{EncryptionKeys: true, Users: true},
		Secrets: &Secrets{ActiveKeyVersion: &version, EncryptionKeys: map[string]string{"2": "k"}},
	})
	index.Sealed = true

	summary := index.Summary()
	if summary.Users != 2 || !summary.SecretsIncluded || !summary.Sealed {
		t.Fatalf("summary = %+v", summary)
	}
	if !summary.Content.EncryptionKeys || !summary.Content.Users {
		t.Fatalf("summary content = %+v", summary.Content)
	}
	// A summary goes to the settings page, so it must not carry key material.
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("encode summary: %v", err)
	}
	if strings.Contains(string(encoded), `"k"`) || strings.Contains(string(encoded), "super-secret") {
		t.Fatalf("the summary leaked key material: %s", encoded)
	}
}

func TestSecretsEmpty(t *testing.T) {
	if !(*Secrets)(nil).Empty() {
		t.Fatal("a nil secrets section is empty")
	}
	if !(&Secrets{}).Empty() {
		t.Fatal("an empty secrets section is empty")
	}
	if (&Secrets{EncryptionKeys: map[string]string{"1": ""}}).Empty() {
		t.Fatal("a section with a key entry is not empty")
	}
}

func TestValidateUsersRejectsWhatCannotBeApplied(t *testing.T) {
	cases := map[string][]User{
		"no id":        {{UserID: 0, Role: "user"}},
		"negative id":  {{UserID: -1, Role: "user"}},
		"duplicate":    {{UserID: 5, Role: "user"}, {UserID: 5, Role: "admin"}},
		"unknown role": {{UserID: 5, Role: "superuser"}},
	}
	for name, users := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateUsers(users); !errors.Is(err, ErrInvalidIndex) {
				t.Fatalf("err = %v, want ErrInvalidIndex", err)
			}
		})
	}
	for name, users := range map[string][]User{
		"empty role": {{UserID: 5}},
		"owner":      {{UserID: 5, Role: "owner"}},
		"admin":      {{UserID: 5, Role: "admin"}},
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			if err := ValidateUsers(users); err != nil {
				t.Fatalf("ValidateUsers: %v", err)
			}
		})
	}
}

// TestValidateRejectsASelectionWithoutItsSection pins the difference between a
// document that says it carries accounts and one that simply has none.
func TestValidateRejectsASelectionWithoutItsSection(t *testing.T) {
	index := goodIndex()
	index.Content.Users = true
	index.Users = nil
	if err := index.Validate(7); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("err = %v, want ErrInvalidIndex", err)
	}

	index = goodIndex()
	index.Content.Users = true
	index.Users = []User{{UserID: 7, Role: "user"}}
	if err := index.Validate(7); err != nil {
		t.Fatalf("a well-formed account list must validate: %v", err)
	}
}

func TestValidateRejectsUnusableSecrets(t *testing.T) {
	negative := int32(-1)
	index := goodIndex()
	index.Secrets = &Secrets{ActiveKeyVersion: &negative}
	if err := index.Validate(7); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("err = %v, want ErrInvalidIndex", err)
	}

	index = goodIndex()
	index.Secrets = &Secrets{EncryptionKeys: map[string]string{"1": "   "}}
	if err := index.Validate(7); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("err = %v, want ErrInvalidIndex", err)
	}
}
