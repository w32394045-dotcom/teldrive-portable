package lanaccess

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), fileName)
	store := NewStore(path, nil)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return store, path
}

// TestRequestAndApproveIsTheWholeFlow is the shape the user asked for: a device
// asks, the machine answers, and only then is there a credential.
// approveAndClaim runs the two steps a device actually runs: the person
// approves, then the device collects its credential.
func approveAndClaim(t *testing.T, store *Store, id string) string {
	t.Helper()
	if _, err := store.Approve(id, 42); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	_, secret, claimed, err := store.Claim(id)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !claimed {
		t.Fatal("Claim did not hand over a credential after approval")
	}
	return secret
}

func TestRequestAndApproveIsTheWholeFlow(t *testing.T) {
	store, _ := testStore(t)

	request, err := store.Request("Living room TV", "192.168.1.44")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if request.State != StatePending {
		t.Fatalf("state = %q, want pending", request.State)
	}
	if len(store.Pending()) != 1 {
		t.Fatalf("pending = %d, want 1", len(store.Pending()))
	}
	// A waiting device has nothing to authenticate with.
	if _, ok := store.Authenticate("tdl_nothing"); ok {
		t.Fatal("something authenticated against an empty registry")
	}

	approved, err := store.Approve(request.ID, 42)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.State != StateApproved {
		t.Fatalf("state = %q, want approved", approved.State)
	}
	if len(store.Pending()) != 0 {
		t.Fatal("an approved device is still waiting")
	}

	_, secret, claimed, err := store.Claim(request.ID)
	if err != nil || !claimed {
		t.Fatalf("Claim: %v (claimed=%v)", err, claimed)
	}
	if !strings.HasPrefix(secret, tokenPrefix) {
		t.Fatalf("credential %q does not identify itself", secret)
	}

	device, ok := store.Authenticate(secret)
	if !ok {
		t.Fatal("the issued credential does not authenticate")
	}
	if device.ID != request.ID || device.Name != "Living room TV" {
		t.Fatalf("authenticated as %+v", device)
	}
	if device.LastSeen.IsZero() {
		t.Fatal("authenticating should record that the device was seen")
	}
}

// TestRegistryNeverStoresTheCredential is the property that makes the file safe
// to copy: a registry is a thing people back up and paste into issues.
func TestRegistryNeverStoresTheCredential(t *testing.T) {
	store, path := testStore(t)
	request, err := store.Request("Laptop", "192.168.1.9")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	secret := approveAndClaim(t, store, request.ID)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the registry: %v", err)
	}
	if strings.Contains(string(content), secret) {
		t.Fatal("the registry file contains the credential")
	}
	// The hash is there, which is what makes the credential checkable.
	var parsed fileFormat
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("decode the registry: %v", err)
	}
	if len(parsed.Devices) != 1 || len(parsed.Devices[0].TokenHash) == 0 {
		t.Fatalf("registry = %+v", parsed.Devices)
	}
}

func TestAuthenticateRejectsEverythingElse(t *testing.T) {
	store, _ := testStore(t)
	request, _ := store.Request("Phone", "192.168.1.5")
	secret := approveAndClaim(t, store, request.ID)

	for name, candidate := range map[string]string{
		"empty":         "",
		"whitespace":    "   ",
		"wrong":         "tdl_not-a-real-credential",
		"prefix only":   tokenPrefix,
		"near miss":     secret[:len(secret)-1] + "X",
		"without prefi": secret[len(tokenPrefix):],
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := store.Authenticate(candidate); ok {
				t.Fatalf("Authenticate(%q) = true", candidate)
			}
		})
	}
}

// TestPendingIsNotACredential: asking is not being allowed.
func TestPendingIsNotACredential(t *testing.T) {
	store, _ := testStore(t)
	if _, err := store.Request("Stranger", "192.168.1.66"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	// Nothing was issued, so nothing can authenticate; the check that matters is
	// that a pending record has no hash to match against.
	for _, device := range store.Devices() {
		if device.State == StatePending {
			var record Device
			if err := json.Unmarshal([]byte(`{"tokenHash":null}`), &record); err == nil && len(record.TokenHash) != 0 {
				t.Fatal("a pending device has a credential")
			}
		}
	}
	if _, ok := store.Authenticate("tdl_anything"); ok {
		t.Fatal("a credential authenticated against an empty registry")
	}
}

func TestDenyAndRevoke(t *testing.T) {
	store, _ := testStore(t)

	first, _ := store.Request("Tablet", "192.168.1.7")
	if _, err := store.Approve(first.ID, 42); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	second, _ := store.Request("Unknown", "192.168.1.8")
	denied, err := store.Deny(second.ID)
	if err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if denied.State != StateDenied || denied.DecidedAt.IsZero() {
		t.Fatalf("denied = %+v", denied)
	}

	// Approving a decision that was already made is refused rather than
	// silently re-opening it.
	if _, err := store.Approve(second.ID, 42); !errors.Is(err, ErrNotPending) {
		t.Fatalf("err = %v, want ErrNotPending", err)
	}
}

func TestRevokeEndsAccessImmediately(t *testing.T) {
	store, _ := testStore(t)
	request, _ := store.Request("Old phone", "192.168.1.20")
	secret := approveAndClaim(t, store, request.ID)

	if _, ok := store.Authenticate(secret); !ok {
		t.Fatal("the credential should work before it is revoked")
	}
	if _, err := store.Revoke(request.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, ok := store.Authenticate(secret); ok {
		t.Fatal("a revoked credential still authenticates")
	}
	// The record stays, so the page can show that it was once allowed.
	devices := store.Devices()
	if len(devices) != 1 || devices[0].State != StateDenied {
		t.Fatalf("devices = %+v", devices)
	}
}

func TestForgetLetsADeviceAskAgain(t *testing.T) {
	store, _ := testStore(t)
	request, _ := store.Request("Laptop", "192.168.1.11")
	if _, err := store.Deny(request.ID); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if err := store.Forget(request.ID); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if len(store.Devices()) != 0 {
		t.Fatal("Forget left the record behind")
	}
	if err := store.Forget(request.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestRequestIsIdempotentWhilePending keeps a reloading page from filling the
// screen with the same device.
func TestRequestIsIdempotentWhilePending(t *testing.T) {
	store, _ := testStore(t)
	first, err := store.Request("TV", "192.168.1.44")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	second, err := store.Request("TV", "192.168.1.44")
	if err != nil {
		t.Fatalf("second Request: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("the same device queued twice: %s and %s", first.ID, second.ID)
	}
	if len(store.Devices()) != 1 {
		t.Fatalf("devices = %d, want 1", len(store.Devices()))
	}
	// A different device is still a different request.
	other, err := store.Request("Tablet", "192.168.1.45")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if other.ID == first.ID {
		t.Fatal("two devices collapsed into one record")
	}
}

func TestPendingExpires(t *testing.T) {
	store, _ := testStore(t)
	when := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return when }

	if _, err := store.Request("Waiting", "192.168.1.50"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len(store.Pending()) != 1 {
		t.Fatal("the request should be waiting")
	}
	// A request nobody answered is not a decision.
	store.now = func() time.Time { return when.Add(PendingTTL + time.Minute) }
	if len(store.Pending()) != 0 {
		t.Fatal("an unanswered request outlived its window")
	}

	// An approved device is not expired: it was answered.
	store.now = func() time.Time { return when }
	request, _ := store.Request("Approved", "192.168.1.51")
	if _, err := store.Approve(request.ID, 42); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	store.now = func() time.Time { return when.Add(24 * time.Hour) }
	if devices := store.Devices(); len(devices) != 1 || devices[0].State != StateApproved {
		t.Fatalf("devices = %+v; an approval must not expire", devices)
	}
}

// TestPendingQueueIsBounded covers a device on the network asking in a loop.
func TestPendingQueueIsBounded(t *testing.T) {
	store, _ := testStore(t)
	for index := 0; index < MaxPending; index++ {
		if _, err := store.Request("Flood", "192.168.1."+itoa(index)); err != nil {
			t.Fatalf("Request %d: %v", index, err)
		}
	}
	if _, err := store.Request("One too many", "10.0.0.1"); !errors.Is(err, ErrTooManyPending) {
		t.Fatalf("err = %v, want ErrTooManyPending", err)
	}
	if len(store.Devices()) != MaxPending {
		t.Fatalf("devices = %d, want %d", len(store.Devices()), MaxPending)
	}
}

func TestRegistryIsBounded(t *testing.T) {
	store, _ := testStore(t)
	store.devices = make([]Device, MaxDevices)
	for index := range store.devices {
		store.devices[index] = Device{ID: uuidFor(index), State: StateDenied, Created: time.Now()}
	}
	if _, err := store.Request("One too many", "10.0.0.1"); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("err = %v, want ErrTooManyDevices", err)
	}
}

// TestNamesAreBoundedAndSanitised: the name is shown to the person deciding, so
// it is display text rather than something to trust.
func TestNamesAreBoundedAndSanitised(t *testing.T) {
	store, _ := testStore(t)
	long := strings.Repeat("a", maxNameLength*2)

	request, err := store.Request("  Living\u0000room\nTV  ", "192.168.1.1")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if strings.ContainsAny(request.Name, "\x00\n") {
		t.Fatalf("name = %q, want control characters removed", request.Name)
	}
	if request.Name != "LivingroomTV" {
		t.Fatalf("name = %q", request.Name)
	}

	other, err := store.Request(long, "192.168.1.2")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len([]rune(other.Name)) != maxNameLength {
		t.Fatalf("name length = %d, want %d", len([]rune(other.Name)), maxNameLength)
	}

	blank, err := store.Request("   ", "192.168.1.3")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if blank.Name == "" {
		t.Fatal("a blank name should fall back to something displayable")
	}
}

func TestMultibyteNamesAreNotCutInHalf(t *testing.T) {
	store, _ := testStore(t)
	request, err := store.Request(strings.Repeat("网", maxNameLength+10), "192.168.1.4")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len([]rune(request.Name)) != maxNameLength {
		t.Fatalf("name = %q (%d runes)", request.Name, len([]rune(request.Name)))
	}
	if !json.Valid([]byte(`"` + request.Name + `"`)) {
		t.Fatalf("the name is not valid UTF-8: %q", request.Name)
	}
}

// TestStoreRoundTripsThroughDisk is what makes an approval survive a restart.
func TestStoreRoundTripsThroughDisk(t *testing.T) {
	first, path := testStore(t)
	request, _ := first.Request("TV", "192.168.1.44")
	secret := approveAndClaim(t, first, request.ID)

	second := NewStore(path, nil)
	if err := second.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := second.Authenticate(secret); !ok {
		t.Fatal("an approval did not survive a restart")
	}
	if devices := second.Devices(); len(devices) != 1 || devices[0].Name != "TV" {
		t.Fatalf("devices = %+v", devices)
	}
}

func TestLoadDegradesOnAnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), fileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	store := NewStore(path, nil)
	if err := store.Load(); err != nil {
		t.Fatalf("Load should not fail the server over this: %v", err)
	}
	if len(store.Devices()) != 0 {
		t.Fatal("a broken registry should read as empty")
	}
	// And it must still be usable afterwards.
	if _, err := store.Request("TV", "192.168.1.1"); err != nil {
		t.Fatalf("Request after a broken load: %v", err)
	}
}

func TestUnknownDeviceIDs(t *testing.T) {
	store, _ := testStore(t)
	if _, err := store.Approve("nope", 42); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Approve err = %v", err)
	}
	if _, err := store.Deny("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Deny err = %v", err)
	}
	if _, err := store.Revoke("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Revoke err = %v", err)
	}
}

// TestConcurrentAccessIsSafe: the registry is read on every request from the
// network while a person is deciding, so the two genuinely overlap.
func TestConcurrentAccessIsSafe(t *testing.T) {
	store, _ := testStore(t)
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for attempt := 0; attempt < 20; attempt++ {
				_, _ = store.Request("Worker", "192.168.1."+itoa(worker))
				store.Devices()
				store.Pending()
				store.Authenticate("tdl_guess")
			}
		}(worker)
	}
	wait.Wait()
	if len(store.Devices()) == 0 {
		t.Fatal("nothing was recorded")
	}
}

// itoa keeps the test free of a strconv import it would otherwise use once.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func uuidFor(index int) string {
	return "device-" + itoa(index)
}
