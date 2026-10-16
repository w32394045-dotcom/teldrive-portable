package backup

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const testPassword = "correct horse battery"

func sealForTest(t *testing.T, plaintext string, password string) []byte {
	t.Helper()
	sealed, err := Seal([]byte(plaintext), password, time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return sealed
}

// TestPasswordIterationsIsTheDocumentedCost pins the work factor. It is the
// single number that decides how expensive guessing is, and it is exactly the
// kind of constant a later performance tweak would quietly lower.
func TestPasswordIterationsIsTheDocumentedCost(t *testing.T) {
	if PasswordIterations != 600_000 {
		t.Fatalf("PasswordIterations = %d, want 600000", PasswordIterations)
	}
	if iterationFloor >= PasswordIterations {
		t.Fatalf("the floor (%d) must sit below the cost we write (%d)", iterationFloor, PasswordIterations)
	}
}

func TestSealRoundTrips(t *testing.T) {
	plaintext := `{"format":"teldrive-index","files":[{"name":"秘密.txt"}]}`
	sealed := sealForTest(t, plaintext, testPassword)

	if !IsSealed(sealed) {
		t.Fatal("a sealed document should identify itself")
	}
	if !IsPasswordProtected(sealed) {
		t.Fatal("a sealed document needs a password")
	}
	opened, err := Unseal(sealed, testPassword)
	if err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	if string(opened) != plaintext {
		t.Fatalf("round trip changed the document:\n%s", opened)
	}
}

// TestSealedDocumentDoesNotLeakItsContents is the property that makes putting
// encryption keys in a snapshot defensible: the channel only ever holds
// ciphertext.
func TestSealedDocumentDoesNotLeakItsContents(t *testing.T) {
	plaintext := `{"format":"teldrive-index","secrets":{"encryptionKeys":{"1":"super-secret-key"}}}`
	sealed := sealForTest(t, plaintext, testPassword)

	if bytes.Contains(sealed, []byte("super-secret-key")) {
		t.Fatal("the sealed document contains the plaintext")
	}
	if bytes.Contains(sealed, []byte("encryptionKeys")) {
		t.Fatal("the sealed document leaks the document structure")
	}
	// The header must stay readable: a person recovering this file years later
	// needs to know what it is without running any code.
	var header map[string]any
	if err := json.Unmarshal(sealed, &header); err != nil {
		t.Fatalf("a sealed document must still be JSON: %v", err)
	}
	if header["format"] != EnvelopeFormat {
		t.Fatalf("format = %v, want %q", header["format"], EnvelopeFormat)
	}
	kdf, ok := header["kdf"].(map[string]any)
	if !ok {
		t.Fatal("the header must describe the key derivation")
	}
	if kdf["algorithm"] != kdfName || kdf["iterations"] != float64(PasswordIterations) {
		t.Fatalf("unexpected kdf header: %v", kdf)
	}
	if header["payload"] == nil {
		t.Fatal("the payload must be present")
	}
}

// TestSealWithAnEmptyPasswordLeavesTheDocumentAlone covers the "password may be
// left blank" case: the snapshot is stored as plain JSON, exactly as before.
func TestSealWithAnEmptyPasswordLeavesTheDocumentAlone(t *testing.T) {
	plaintext := `{"format":"teldrive-index"}`
	sealed, err := Seal([]byte(plaintext), "", time.Now())
	if err != nil {
		t.Fatalf("Seal with an empty password: %v", err)
	}
	if string(sealed) != plaintext {
		t.Fatalf("an empty password must not transform the document, got %s", sealed)
	}
	if IsSealed(sealed) {
		t.Fatal("an unencrypted document must not claim to be sealed")
	}
	opened, err := Unseal(sealed, "")
	if err != nil {
		t.Fatalf("Unseal of a plain document: %v", err)
	}
	if string(opened) != plaintext {
		t.Fatal("a plain document must pass through unchanged")
	}
}

func TestSealRejectsAWeakOrAbsurdPassword(t *testing.T) {
	for name, password := range map[string]string{
		"one character":   "a",
		"just under the":  strings.Repeat("x", MinimumPasswordLength-1),
		"far too long":    strings.Repeat("x", MaximumPasswordLength+1),
		"only whitespace": strings.Repeat(" ", MinimumPasswordLength),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Seal([]byte("{}"), password, time.Now()); err == nil {
				t.Fatalf("Seal accepted a password it should have refused (len %d)", len(password))
			}
		})
	}
}

func TestUnsealRejectsAWrongPassword(t *testing.T) {
	sealed := sealForTest(t, `{"format":"teldrive-index"}`, testPassword)
	for name, attempt := range map[string]string{
		"different":  "correct horse batteries",
		"empty":      "",
		"very close": "correct horse battery ",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Unseal(sealed, attempt)
			if attempt == "" {
				if !errors.Is(err, ErrPasswordRequired) {
					t.Fatalf("err = %v, want ErrPasswordRequired", err)
				}
				return
			}
			if !errors.Is(err, ErrPasswordRejected) {
				t.Fatalf("err = %v, want ErrPasswordRejected", err)
			}
		})
	}
}

// TestUnsealRejectsATamperedPayload covers the integrity half of AES-GCM: a
// single flipped bit in the ciphertext must not decrypt.
func TestUnsealRejectsATamperedPayload(t *testing.T) {
	sealed := sealForTest(t, `{"format":"teldrive-index"}`, testPassword)
	var file envelopeFile
	if err := json.Unmarshal(sealed, &file); err != nil {
		t.Fatalf("decode sealed document: %v", err)
	}
	payload, err := base64.StdEncoding.DecodeString(file.Payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	payload[len(payload)/2] ^= 0x01
	file.Payload = base64.StdEncoding.EncodeToString(payload)
	tampered, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("encode tampered document: %v", err)
	}
	if _, err := Unseal(tampered, testPassword); !errors.Is(err, ErrPasswordRejected) {
		t.Fatalf("err = %v, want ErrPasswordRejected", err)
	}
}

// TestUnsealRejectsATamperedHeader is why the header is authenticated data. A
// stored file must not be editable into a cheaper-to-crack one, or into one
// that claims a different algorithm.
func TestUnsealRejectsATamperedHeader(t *testing.T) {
	sealed := sealForTest(t, `{"format":"teldrive-index"}`, testPassword)

	rewrite := func(mutate func(header map[string]any)) []byte {
		var document map[string]any
		if err := json.Unmarshal(sealed, &document); err != nil {
			t.Fatalf("decode sealed document: %v", err)
		}
		mutate(document)
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encode tampered document: %v", err)
		}
		return encoded
	}

	t.Run("weakened cost", func(t *testing.T) {
		tampered := rewrite(func(document map[string]any) {
			document["kdf"].(map[string]any)["iterations"] = float64(iterationFloor)
		})
		if _, err := Unseal(tampered, testPassword); !errors.Is(err, ErrPasswordRejected) {
			t.Fatalf("err = %v, want ErrPasswordRejected", err)
		}
	})

	t.Run("cost below the floor", func(t *testing.T) {
		tampered := rewrite(func(document map[string]any) {
			document["kdf"].(map[string]any)["iterations"] = float64(1)
		})
		if _, err := Unseal(tampered, testPassword); !errors.Is(err, ErrEnvelopeInvalid) {
			t.Fatalf("err = %v, want ErrEnvelopeInvalid", err)
		}
	})

	t.Run("cost above the ceiling", func(t *testing.T) {
		tampered := rewrite(func(document map[string]any) {
			document["kdf"].(map[string]any)["iterations"] = float64(iterationCeiling + 1)
		})
		if _, err := Unseal(tampered, testPassword); !errors.Is(err, ErrEnvelopeInvalid) {
			t.Fatalf("err = %v, want ErrEnvelopeInvalid", err)
		}
	})

	t.Run("swapped cipher", func(t *testing.T) {
		tampered := rewrite(func(document map[string]any) {
			document["cipher"].(map[string]any)["algorithm"] = "AES-128-CBC"
		})
		if _, err := Unseal(tampered, testPassword); !errors.Is(err, ErrEnvelopeInvalid) {
			t.Fatalf("err = %v, want ErrEnvelopeInvalid", err)
		}
	})

	t.Run("unknown container version", func(t *testing.T) {
		tampered := rewrite(func(document map[string]any) {
			document["version"] = float64(EnvelopeVersion + 1)
		})
		if _, err := Unseal(tampered, testPassword); !errors.Is(err, ErrEnvelopeInvalid) {
			t.Fatalf("err = %v, want ErrEnvelopeInvalid", err)
		}
	})

	t.Run("mangled salt", func(t *testing.T) {
		tampered := rewrite(func(document map[string]any) {
			document["kdf"].(map[string]any)["salt"] = base64.StdEncoding.EncodeToString([]byte("short"))
		})
		if _, err := Unseal(tampered, testPassword); !errors.Is(err, ErrEnvelopeInvalid) {
			t.Fatalf("err = %v, want ErrEnvelopeInvalid", err)
		}
	})
}

// TestSealIsNotDeterministic checks the salt and nonce are actually random: two
// seals of the same document must not produce the same bytes, or the same
// plaintext would be recognisable across snapshots.
func TestSealIsNotDeterministic(t *testing.T) {
	plaintext := `{"format":"teldrive-index","counts":{"files":1}}`
	first := sealForTest(t, plaintext, testPassword)
	second := sealForTest(t, plaintext, testPassword)
	if bytes.Equal(first, second) {
		t.Fatal("two seals of the same document are identical")
	}
	for _, document := range [][]byte{first, second} {
		if opened, err := Unseal(document, testPassword); err != nil || string(opened) != plaintext {
			t.Fatalf("re-sealed document did not round trip: %v", err)
		}
	}
}

func TestIsSealedIgnoresEverythingElse(t *testing.T) {
	if IsSealed([]byte(`{"format":"teldrive-index"}`)) {
		t.Fatal("a plain index must not be reported as sealed")
	}
	for name, document := range map[string]string{
		"not json":     "hello",
		"empty":        "",
		"array":        "[]",
		"missing":      "{}",
		"unrelated":    `{"format":"something-else"}`,
		"nearly there": `{"format":"teldrive-index-seale"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if IsSealed([]byte(document)) {
				t.Fatalf("IsSealed(%q) = true", document)
			}
		})
	}
}

func TestGeneratePasswordIsUsableAsAKey(t *testing.T) {
	for _, length := range []int{GeneratedPasswordMin, 20, GeneratedPasswordMax} {
		password, err := GeneratePassword(length)
		if err != nil {
			t.Fatalf("GeneratePassword(%d): %v", length, err)
		}
		if len(password) != length {
			t.Fatalf("generated %d characters, want %d", len(password), length)
		}
		for _, character := range password {
			if !strings.ContainsRune(passwordAlphabet, character) {
				t.Fatalf("generated password contains %q, which is not in the alphabet", character)
			}
		}
		if _, err := Seal([]byte("{}"), password, time.Now()); err != nil {
			t.Fatalf("a generated password must be accepted by Seal: %v", err)
		}
	}
}

func TestGeneratePasswordClampsTheLength(t *testing.T) {
	short, err := GeneratePassword(1)
	if err != nil {
		t.Fatalf("GeneratePassword(1): %v", err)
	}
	if len(short) != GeneratedPasswordMin {
		t.Fatalf("length = %d, want the minimum %d", len(short), GeneratedPasswordMin)
	}
	long, err := GeneratePassword(500)
	if err != nil {
		t.Fatalf("GeneratePassword(500): %v", err)
	}
	if len(long) != GeneratedPasswordMax {
		t.Fatalf("length = %d, want the maximum %d", len(long), GeneratedPasswordMax)
	}
}

func TestGeneratePasswordDoesNotRepeat(t *testing.T) {
	seen := make(map[string]bool, 64)
	for attempt := 0; attempt < 64; attempt++ {
		password, err := GeneratePassword(GeneratedPasswordMin)
		if err != nil {
			t.Fatalf("GeneratePassword: %v", err)
		}
		if seen[password] {
			t.Fatalf("GeneratePassword returned %q twice", password)
		}
		seen[password] = true
	}
}
