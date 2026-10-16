package backup

import "fmt"

// Content selects the optional sections a snapshot carries.
//
// The core sections -- channels, files and parts -- are not negotiable and never
// appear here: a snapshot without them could not rebuild anything, so offering a
// switch to leave them out would only offer a way to make a useless backup.
// What is optional is everything beyond the catalog.
type Content struct {
	// EncryptionKeys includes the server-managed keys that decrypt the stored
	// parts. Without them, encrypted files survive a database loss as ciphertext
	// nobody can read, which is the specific gap this whole feature exists for.
	// It is also the most dangerous thing a snapshot can carry, so the settings
	// page is expected to warn when it is on and the file is not sealed.
	EncryptionKeys bool `json:"encryptionKeys"`
	// Users includes the account rows. A catalog references them, so rebuilding
	// a drive on a fresh database needs them before anything else can be put
	// back.
	Users bool `json:"users"`
	// VaultKey includes the key that opens the instance's settings vault. It is
	// the one section that is refused outright unless the snapshot is also
	// sealed: on its own it is the key to every other secret in the same file,
	// sitting in a Telegram channel.
	VaultKey bool `json:"vaultKey"`
}

// Any reports whether at least one optional section is selected.
func (c Content) Any() bool { return c.EncryptionKeys || c.Users || c.VaultKey }

// RequiresSealing reports whether the selection may only be written inside a
// sealed snapshot.
func (c Content) RequiresSealing() bool { return c.VaultKey }

// Secrets is the server-side configuration a rebuild needs and the database does
// not hold. It is written to config.toml, not to a table, which is exactly why
// losing the machine loses it.
type Secrets struct {
	// ActiveKeyVersion is the version new uploads are encrypted with.
	ActiveKeyVersion *int32 `json:"activeKeyVersion,omitempty"`
	// EncryptionKeys maps key version to key material. The values are secrets in
	// the strictest sense: with them, every stored part can be decrypted.
	EncryptionKeys map[string]string `json:"encryptionKeys,omitempty"`
}

// Empty reports whether there is nothing worth writing.
func (s *Secrets) Empty() bool {
	return s == nil || (s.ActiveKeyVersion == nil && len(s.EncryptionKeys) == 0)
}

// User is one account row. Role travels with it so a rebuild can restore who
// administers the instance, but see the restore notes: an apply never changes
// the role of an account that already exists.
type User struct {
	UserID      int64  `json:"userId"`
	DisplayName string `json:"displayName,omitempty"`
	Username    string `json:"username,omitempty"`
	Premium     bool   `json:"premium,omitempty"`
	Role        string `json:"role,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
}

// ValidateUsers rejects an account list that could not be applied.
func ValidateUsers(users []User) error {
	seen := make(map[int64]bool, len(users))
	for _, user := range users {
		if user.UserID <= 0 {
			return fmt.Errorf("%w: an account has no user id", ErrInvalidIndex)
		}
		if seen[user.UserID] {
			return fmt.Errorf("%w: account %d appears twice", ErrInvalidIndex, user.UserID)
		}
		seen[user.UserID] = true
		switch user.Role {
		case "", "owner", "admin", "user":
		default:
			return fmt.Errorf("%w: account %d has unknown role %q", ErrInvalidIndex, user.UserID, user.Role)
		}
	}
	return nil
}
