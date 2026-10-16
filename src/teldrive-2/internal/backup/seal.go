package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Sealing protects a snapshot with a password.
//
// A snapshot is the one file that can rebuild the whole drive, and it is stored
// in a Telegram channel next to the data it describes. Leaving it as readable
// JSON means anyone who can read the channel learns which message holds which
// file, and -- when the snapshot carries the server-managed keys -- how to
// decrypt all of it. Sealing is what makes putting those keys in the backup
// defensible at all.
//
// The container stays JSON on purpose. A disaster-recovery artefact should be
// something a person can open in a text editor and recognise: this is a Teldrive
// index, version 1, sealed with PBKDF2-SHA256 at so many iterations. The only
// opaque part is the payload.
const (
	// EnvelopeFormat marks a sealed document, so a reader can tell it apart from
	// a plain index without guessing.
	EnvelopeFormat = "teldrive-index-sealed"
	// EnvelopeVersion is the container layout. It changes only if a reader would
	// have to interpret the file differently.
	EnvelopeVersion = 1

	kdfName    = "PBKDF2-SHA256"
	cipherName = "AES-256-GCM"

	// PasswordIterations is the PBKDF2 work factor for password mode. OWASP's
	// floor for PBKDF2-SHA256 is 600,000, and this file may sit in a chat
	// history for years, so it is not a number to tune down for speed.
	PasswordIterations = 600_000
	// iterationFloor rejects a header that asks for less work than we are
	// willing to do. Without it, an attacker who can edit the stored document
	// could rewrite the cost down and brute-force the password cheaply.
	iterationFloor = 100_000
	// iterationCeiling rejects a header that asks for so much work that opening
	// the file would hang the server. A denial of service is not a compromise,
	// but it is still a way to take the instance down from a chat message.
	iterationCeiling = 5_000_000

	sealSaltSize  = 16
	sealKeySize   = 32
	sealNonceSize = 12

	// MinimumPasswordLength is deliberately low: this is a guard against a
	// mistyped key, not a policy. A short password is the user's call, but a
	// one-character one is almost certainly an accident.
	MinimumPasswordLength = 8
	// MaximumPasswordLength keeps a pasted novel from turning into a memory
	// problem inside the KDF.
	MaximumPasswordLength = 1024

	// GeneratedPasswordMin and GeneratedPasswordMax bound the random tokens the
	// settings page offers.
	GeneratedPasswordMin = 14
	GeneratedPasswordMax = 25
)

var (
	// ErrPasswordRequired reports a sealed snapshot opened without a password.
	ErrPasswordRequired = errors.New("backup: this snapshot is encrypted and needs a password")
	// ErrPasswordRejected reports a password that did not authenticate. It is
	// the same error for a wrong password and for a tampered payload, because
	// AES-GCM genuinely cannot tell them apart, and guessing would be worse
	// than saying so.
	ErrPasswordRejected = errors.New("backup: the password does not match, or the snapshot was altered")
	// ErrPasswordTooShort and ErrPasswordTooLong describe an unusable password.
	ErrPasswordTooShort = fmt.Errorf("backup: the password must be at least %d characters", MinimumPasswordLength)
	ErrPasswordTooLong  = fmt.Errorf("backup: the password must be at most %d characters", MaximumPasswordLength)
	// ErrPasswordBlank reports a password made only of whitespace. It is refused
	// rather than trimmed: trimming would silently change a password that is
	// legitimately allowed to have spaces, while a key that is *only* spaces is
	// a stray keystroke, and one no keyboard will reproduce reliably.
	ErrPasswordBlank = errors.New("backup: the password cannot be only spaces")
	// ErrEnvelopeInvalid reports a sealed container that cannot be trusted.
	ErrEnvelopeInvalid = errors.New("backup: the encrypted snapshot header is not usable")
)

// kdfParams describe how the password became a key. They are stored in the file
// so a future reader does not need this code to know how to derive it.
type kdfParams struct {
	Algorithm  string `json:"algorithm"`
	Hash       string `json:"hash"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
}

type cipherParams struct {
	Algorithm string `json:"algorithm"`
	Nonce     string `json:"nonce"`
}

// envelopeHeader is everything except the payload. It is also the additional
// authenticated data for the cipher, so none of these values can be edited
// without the password check failing -- which is what stops a stored file from
// being re-labelled as "encrypted with 1000 iterations" or "not encrypted".
type envelopeHeader struct {
	Format  string       `json:"format"`
	Version int          `json:"version"`
	Sealed  time.Time    `json:"sealedAt"`
	KDF     kdfParams    `json:"kdf"`
	Cipher  cipherParams `json:"cipher"`
}

type envelopeFile struct {
	envelopeHeader
	Payload string `json:"payload"`
}

func (h envelopeHeader) additionalData() ([]byte, error) {
	// Marshalling the struct is deterministic, so the same header always yields
	// the same bytes; that is what makes it usable as associated data.
	return json.Marshal(h)
}

// EnvelopeInfo describes a sealed container without opening it.
type EnvelopeInfo struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	SealedAt   time.Time `json:"sealedAt"`
	Iterations int       `json:"iterations"`
}

// SealMetadata reads a container's header without decrypting it.
//
// It exists so a caller can report what a sealed file is -- and, for the vault,
// so the settings page can say whether anything is sealed without being able to
// read it. The header is not trusted for anything but reporting: the password
// check and the associated-data binding are what make it authoritative.
func SealMetadata(format string, document []byte) (EnvelopeInfo, error) {
	var file envelopeFile
	if err := json.Unmarshal(document, &file); err != nil {
		return EnvelopeInfo{}, fmt.Errorf("%w: %v", ErrEnvelopeInvalid, err)
	}
	if file.Format != format {
		return EnvelopeInfo{}, fmt.Errorf("%w: unknown format %q", ErrEnvelopeInvalid, file.Format)
	}
	return EnvelopeInfo{
		Format:     file.Format,
		Version:    file.Version,
		SealedAt:   file.Sealed,
		Iterations: file.KDF.Iterations,
	}, nil
}

// IsSealed reports whether a stored document is a sealed container.
func IsSealed(document []byte) bool { return IsSealedAs(EnvelopeFormat, document) }

// IsSealedAs is IsSealed for a container that names itself something else, so a
// reader can tell a sealed snapshot from a sealed vault without guessing.
func IsSealedAs(format string, document []byte) bool {
	var probe struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(document, &probe); err != nil {
		return false
	}
	return probe.Format == format
}

// SealAs seals a document under a different container name.
//
// The vault protects the instance's own secrets rather than a snapshot, and a
// file that says it is a Teldrive index when it is not would be a trap for
// whoever opens it during a recovery. The cryptography is deliberately shared:
// one implementation of the envelope is one place for a mistake to be.
func SealAs(format string, plaintext []byte, password string, now time.Time) ([]byte, error) {
	return sealAs(format, EnvelopeVersion, plaintext, password, now)
}

// Seal encrypts a snapshot with a password.
//
// An empty password returns the document untouched: snapshots may legitimately
// be unencrypted, and pretending otherwise would either break existing backups
// or silently invent a key nobody knows. Callers that care are expected to warn
// the user instead.
func Seal(plaintext []byte, password string, now time.Time) ([]byte, error) {
	return SealAs(EnvelopeFormat, plaintext, password, now)
}

func sealAs(format string, version int, plaintext []byte, password string, now time.Time) ([]byte, error) {
	if password == "" {
		return plaintext, nil
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	salt, err := randomBytes(sealSaltSize)
	if err != nil {
		return nil, fmt.Errorf("backup: generate salt: %w", err)
	}
	key, err := deriveKey(password, salt, PasswordIterations)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: create GCM: %w", err)
	}
	nonce, err := randomBytes(gcm.NonceSize())
	if err != nil {
		return nil, fmt.Errorf("backup: generate nonce: %w", err)
	}

	header := envelopeHeader{
		Format:  format,
		Version: version,
		Sealed:  now.UTC(),
		KDF: kdfParams{
			Algorithm:  kdfName,
			Hash:       "SHA-256",
			Iterations: PasswordIterations,
			Salt:       base64.StdEncoding.EncodeToString(salt),
		},
		Cipher: cipherParams{
			Algorithm: cipherName,
			Nonce:     base64.StdEncoding.EncodeToString(nonce),
		},
	}
	associated, err := header.additionalData()
	if err != nil {
		return nil, fmt.Errorf("backup: encode header: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plaintext, associated)

	document, err := json.MarshalIndent(envelopeFile{
		envelopeHeader: header,
		Payload:        base64.StdEncoding.EncodeToString(sealed),
	}, "", " ")
	if err != nil {
		return nil, fmt.Errorf("backup: encode sealed snapshot: %w", err)
	}
	return append(document, '\n'), nil
}

// Unseal decrypts a sealed snapshot.
//
// A document that is not sealed is returned as-is, so this is safe to call on
// every load rather than making callers detect the format first.
func Unseal(document []byte, password string) ([]byte, error) {
	return UnsealAs(EnvelopeFormat, document, password)
}

// UnsealAs is Unseal for a container that names itself something else.
func UnsealAs(format string, document []byte, password string) ([]byte, error) {
	return unsealAs(format, EnvelopeVersion, document, password)
}

func unsealAs(format string, version int, document []byte, password string) ([]byte, error) {
	if !IsSealedAs(format, document) {
		return document, nil
	}
	var file envelopeFile
	if err := json.Unmarshal(document, &file); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEnvelopeInvalid, err)
	}
	header := file.envelopeHeader
	if header.Format != format {
		return nil, fmt.Errorf("%w: unknown format %q", ErrEnvelopeInvalid, header.Format)
	}
	if header.Version != version {
		return nil, fmt.Errorf("%w: container version %d is not supported", ErrEnvelopeInvalid, header.Version)
	}
	if header.KDF.Algorithm != kdfName || !strings.EqualFold(header.KDF.Hash, "SHA-256") {
		return nil, fmt.Errorf("%w: unknown key derivation %q", ErrEnvelopeInvalid, header.KDF.Algorithm)
	}
	if header.KDF.Iterations < iterationFloor || header.KDF.Iterations > iterationCeiling {
		return nil, fmt.Errorf("%w: %d iterations is outside the accepted range",
			ErrEnvelopeInvalid, header.KDF.Iterations)
	}
	if header.Cipher.Algorithm != cipherName {
		return nil, fmt.Errorf("%w: unknown cipher %q", ErrEnvelopeInvalid, header.Cipher.Algorithm)
	}
	salt, err := base64.StdEncoding.DecodeString(header.KDF.Salt)
	if err != nil || len(salt) != sealSaltSize {
		return nil, fmt.Errorf("%w: malformed salt", ErrEnvelopeInvalid)
	}
	nonce, err := base64.StdEncoding.DecodeString(header.Cipher.Nonce)
	if err != nil || len(nonce) == 0 {
		return nil, fmt.Errorf("%w: malformed nonce", ErrEnvelopeInvalid)
	}
	payload, err := base64.StdEncoding.DecodeString(file.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed payload", ErrEnvelopeInvalid)
	}
	// Check the password before spending 600,000 iterations' worth of work on
	// deriving a key we cannot use.
	if password == "" {
		return nil, ErrPasswordRequired
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}

	key, err := deriveKey(password, salt, header.KDF.Iterations)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: create GCM: %w", err)
	}
	associated, err := header.additionalData()
	if err != nil {
		return nil, fmt.Errorf("backup: encode header: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, payload, associated)
	if err != nil {
		return nil, ErrPasswordRejected
	}
	return plaintext, nil
}

// IsPasswordProtected reports whether a sealed document needs a password, which
// lets the settings page ask before it tries.
func IsPasswordProtected(document []byte) bool { return IsSealed(document) }

// ValidatePassword reports whether a passphrase can be used for sealing. An
// empty passphrase is valid and means "do not seal", so the settings endpoint
// can apply a submitted value without special-casing the empty one.
func ValidatePassword(password string) error {
	if password == "" {
		return nil
	}
	return validatePassword(password)
}

func validatePassword(password string) error {
	if len(password) < MinimumPasswordLength {
		return ErrPasswordTooShort
	}
	if len(password) > MaximumPasswordLength {
		return ErrPasswordTooLong
	}
	if strings.TrimSpace(password) == "" {
		return ErrPasswordBlank
	}
	return nil
}

// deriveKey runs PBKDF2-SHA256. The cost is the whole point of the function, so
// it is deliberately not cached or shortened.
func deriveKey(password string, salt []byte, iterations int) ([]byte, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, sealKeySize)
	if err != nil {
		return nil, fmt.Errorf("backup: derive key: %w", err)
	}
	return key, nil
}

// passwordAlphabet leaves out the characters people misread when copying a key
// out of a screenshot or off a printed page: 0/O, 1/l/I.
const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword returns a random token of the requested length.
//
// It is offered so a user who has no password manager can still get a key that
// is worth the 600,000 iterations protecting it. The alphabet is 57 characters,
// so a 14-character token is about 81 bits and a 25-character one about 146.
func GeneratePassword(length int) (string, error) {
	if length < GeneratedPasswordMin {
		length = GeneratedPasswordMin
	}
	if length > GeneratedPasswordMax {
		length = GeneratedPasswordMax
	}
	limit := big.NewInt(int64(len(passwordAlphabet)))
	var builder strings.Builder
	builder.Grow(length)
	for index := 0; index < length; index++ {
		// Rejection-free: big.Int.Int is uniform over [0, limit), so every
		// character is equally likely and there is no modulo bias.
		picked, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("backup: generate password: %w", err)
		}
		builder.WriteByte(passwordAlphabet[picked.Int64()])
	}
	return builder.String(), nil
}

func randomBytes(size int) ([]byte, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return nil, err
	}
	return buffer, nil
}
