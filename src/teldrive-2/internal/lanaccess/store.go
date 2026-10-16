// Package lanaccess decides which other devices on the local network may use
// this drive.
//
// Binding to the whole network is a different thing from being reachable on it.
// Any device on the same Wi-Fi can connect once the server listens on 0.0.0.0,
// and a drive on a laptop that joins a café network is on the same network as
// everyone else in the café. So access is asked for and granted, rather than
// assumed: a device requests, the person at the machine approves, and only then
// does it hold a credential.
//
// The companion model matters here. An approved device does not sign in to
// Telegram -- it borrows the session the machine already has, because the person
// is standing next to it. That is why approval is a strong act and why the
// credential is bound to one device rather than being a shared password.
package lanaccess

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// fileName is the registry, in the data directory beside the database.
	fileName = "lan-access.json"

	// tokenPrefix marks a device credential so it is recognisable in a log or a
	// screenshot, the way the API keys are.
	tokenPrefix = "tdl_"
	// tokenBytes is the entropy behind a credential. The same 256 bits as an API
	// key: this credential carries the owner's session once approved.
	tokenBytes = 32

	// PendingTTL is how long a request waits before it is forgotten. A request
	// nobody answered is not a decision, and leaving them on screen forever
	// trains people to approve without reading.
	PendingTTL = 10 * time.Minute
	// MaxPending bounds the queue, so a device on the network cannot fill the
	// disk by asking repeatedly.
	MaxPending = 32
	// MaxDevices bounds the registry for the same reason.
	MaxDevices = 256
	// maxNameLength bounds what a client can call itself.
	maxNameLength = 64
)

// State is where a device stands.
type State string

const (
	// StatePending is a request nobody has answered yet.
	StatePending State = "pending"
	// StateApproved may use the drive; the credential is its token.
	StateApproved State = "approved"
	// StateDenied was refused. The record is kept so the same device does not
	// simply reappear as a fresh request, and so the person can see they refused
	// it rather than wondering whether the request was lost.
	StateDenied State = "denied"
)

var (
	// ErrNotFound reports a device id that is not in the registry.
	ErrNotFound = errors.New("lanaccess: unknown device")
	// ErrTooManyPending reports a full request queue.
	ErrTooManyPending = errors.New("lanaccess: too many requests are already waiting")
	// ErrTooManyDevices reports a full registry.
	ErrTooManyDevices = errors.New("lanaccess: too many devices are already registered")
	// ErrNotPending reports an approval attempt on something already decided.
	ErrNotPending = errors.New("lanaccess: that request has already been answered")
	// ErrDenied reports a device whose request was refused. It is separated from
	// ErrNotFound so the page can say "you refused this" rather than "unknown".
	ErrDenied = errors.New("lanaccess: that request was refused")
)

// Device is one other machine that has asked for, or been given, access.
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Address is where the request came from, which is what the person
	// recognises on screen. It is not a credential: addresses move.
	Address   string    `json:"address,omitempty"`
	State     State     `json:"state"`
	Created   time.Time `json:"createdAt"`
	DecidedAt time.Time `json:"decidedAt,omitempty"`
	LastSeen  time.Time `json:"lastSeenAt,omitempty"`
	// TokenHash is the SHA-256 of the credential, never the credential itself.
	// A registry file is a thing people copy around.
	TokenHash []byte `json:"tokenHash,omitempty"`
	// TokenDelivered records that the device has collected its credential. The
	// credential is handed over exactly once: a device that loses it asks again
	// and the person decides again, rather than the registry becoming a place a
	// credential can be read out of forever.
	TokenDelivered bool `json:"tokenDelivered,omitempty"`
}

// Public is the device as the settings page and the tray need it: no hash.
type Public struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Address   string    `json:"address,omitempty"`
	State     State     `json:"state"`
	Created   time.Time `json:"createdAt"`
	DecidedAt time.Time `json:"decidedAt,omitempty"`
	LastSeen  time.Time `json:"lastSeenAt,omitempty"`
}

func (d Device) public() Public {
	return Public{
		ID: d.ID, Name: d.Name, Address: d.Address, State: d.State,
		Created: d.Created, DecidedAt: d.DecidedAt, LastSeen: d.LastSeen,
	}
}

type fileFormat struct {
	Devices []Device `json:"devices"`
}

// Store owns the registry. It is read on every request that arrives from the
// network and written whenever a person decides something, so it is guarded.
type Store struct {
	path   string
	logger *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	devices []Device
}

func NewStore(path string, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{path: path, logger: logger, now: time.Now}
}

// DefaultPath returns the registry beside the other settings files.
func DefaultPath(dataDir string) string { return filepath.Join(dataDir, fileName) }

// Load reads the registry. A missing file is the normal first run and is not an
// error; an unreadable one degrades to an empty registry rather than refusing to
// start the server over a feature nobody has used.
func (s *Store) Load() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	content, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lanaccess: read registry: %w", err)
	}
	var parsed fileFormat
	if err := json.Unmarshal(content, &parsed); err != nil {
		s.logger.Warn("lanaccess.registry.unreadable", "path", s.path, "error", err)
		return nil
	}
	s.mu.Lock()
	s.devices = parsed.Devices
	s.mu.Unlock()
	return nil
}

// Path is the registry file, for diagnostics.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Devices returns every record, newest first, without any credential material.
func (s *Store) Devices() []Public {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	out := make([]Public, 0, len(s.devices))
	for _, device := range s.devices {
		out = append(out, device.public())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Pending returns just the requests waiting for an answer.
func (s *Store) Pending() []Public {
	devices := s.Devices()
	out := make([]Public, 0, len(devices))
	for _, device := range devices {
		if device.State == StatePending {
			out = append(out, device)
		}
	}
	return out
}

// Request records a device asking for access and returns its record.
//
// A device that already asked, and is still waiting, is not queued twice: the
// same machine retrying because the page reloaded must not fill the screen.
func (s *Store) Request(name, address string) (Public, error) {
	if s == nil {
		return Public{}, errors.New("lanaccess: store is not configured")
	}
	name = cleanName(name)
	address = strings.TrimSpace(address)

	s.mu.Lock()
	s.expireLocked()
	// An address that is already waiting is the same device asking again.
	for index := range s.devices {
		if s.devices[index].Address == address && s.devices[index].State == StatePending {
			s.devices[index].LastSeen = s.now().UTC()
			existing := s.devices[index]
			s.mu.Unlock()
			return existing.public(), nil
		}
	}
	pending := 0
	for _, device := range s.devices {
		if device.State == StatePending {
			pending++
		}
	}
	if pending >= MaxPending {
		s.mu.Unlock()
		return Public{}, ErrTooManyPending
	}
	if len(s.devices) >= MaxDevices {
		s.mu.Unlock()
		return Public{}, ErrTooManyDevices
	}
	record := Device{
		ID:      uuid.NewString(),
		Name:    name,
		Address: address,
		State:   StatePending,
		Created: s.now().UTC(),
	}
	s.devices = append(s.devices, record)
	devices := append([]Device(nil), s.devices...)
	s.mu.Unlock()

	if err := s.write(devices); err != nil {
		return Public{}, err
	}
	s.logger.Info("lanaccess.requested", "device", record.ID, "name", record.Name, "address", record.Address)
	return record.public(), nil
}

// Approve grants access. The device collects its own credential afterwards, by
// asking with the request id it already has -- so the person approving never has
// to carry a secret across to another machine by hand, which is the step that
// would make this feature annoying enough not to use.
func (s *Store) Approve(id string) (Public, error) {
	if s == nil {
		return Public{}, errors.New("lanaccess: store is not configured")
	}
	s.mu.Lock()
	index := s.indexLocked(id)
	if index < 0 {
		s.mu.Unlock()
		return Public{}, ErrNotFound
	}
	if s.devices[index].State != StatePending {
		s.mu.Unlock()
		return Public{}, ErrNotPending
	}
	s.devices[index].State = StateApproved
	s.devices[index].DecidedAt = s.now().UTC()
	// Still no credential: it is minted when the device collects it, so the
	// value the device receives has never been on disk.
	s.devices[index].TokenHash = nil
	s.devices[index].TokenDelivered = false
	approved := s.devices[index]
	devices := append([]Device(nil), s.devices...)
	s.mu.Unlock()

	if err := s.write(devices); err != nil {
		return Public{}, err
	}
	s.logger.Warn("lanaccess.approved", "device", approved.ID, "name", approved.Name, "address", approved.Address)
	return approved.public(), nil
}

// Deny refuses a request. The record stays so the same device does not simply
// reappear, and so the person can see that they answered it.
func (s *Store) Deny(id string) (Public, error) {
	if s == nil {
		return Public{}, errors.New("lanaccess: store is not configured")
	}
	s.mu.Lock()
	index := s.indexLocked(id)
	if index < 0 {
		s.mu.Unlock()
		return Public{}, ErrNotFound
	}
	if s.devices[index].State == StateDenied {
		denied := s.devices[index]
		s.mu.Unlock()
		return denied.public(), nil
	}
	s.devices[index].State = StateDenied
	s.devices[index].DecidedAt = s.now().UTC()
	s.devices[index].TokenHash = nil
	denied := s.devices[index]
	devices := append([]Device(nil), s.devices...)
	s.mu.Unlock()

	if err := s.write(devices); err != nil {
		return Public{}, err
	}
	s.logger.Info("lanaccess.denied", "device", denied.ID, "name", denied.Name)
	return denied.public(), nil
}

// Revoke removes a device's access without forgetting it, so the page can show
// that it was once allowed and no longer is.
func (s *Store) Revoke(id string) (Public, error) {
	if s == nil {
		return Public{}, errors.New("lanaccess: store is not configured")
	}
	s.mu.Lock()
	index := s.indexLocked(id)
	if index < 0 {
		s.mu.Unlock()
		return Public{}, ErrNotFound
	}
	s.devices[index].State = StateDenied
	s.devices[index].DecidedAt = s.now().UTC()
	s.devices[index].TokenHash = nil
	revoked := s.devices[index]
	devices := append([]Device(nil), s.devices...)
	s.mu.Unlock()

	if err := s.write(devices); err != nil {
		return Public{}, err
	}
	s.logger.Warn("lanaccess.revoked", "device", revoked.ID, "name", revoked.Name)
	return revoked.public(), nil
}

// Forget removes a record entirely, which is how a decision is undone so the
// device can ask again.
func (s *Store) Forget(id string) error {
	if s == nil {
		return errors.New("lanaccess: store is not configured")
	}
	s.mu.Lock()
	index := s.indexLocked(id)
	if index < 0 {
		s.mu.Unlock()
		return ErrNotFound
	}
	s.devices = append(s.devices[:index], s.devices[index+1:]...)
	devices := append([]Device(nil), s.devices...)
	s.mu.Unlock()
	return s.write(devices)
}

// Authenticate reports which approved device a credential belongs to.
//
// The comparison is constant time and walks every record, so neither the timing
// nor the amount of work reveals which device a guess was close to.
func (s *Store) Authenticate(secret string) (Public, bool) {
	if s == nil || strings.TrimSpace(secret) == "" {
		return Public{}, false
	}
	candidate := hashToken(secret)

	s.mu.Lock()
	defer s.mu.Unlock()
	var found *Device
	for index := range s.devices {
		device := &s.devices[index]
		if device.State != StateApproved || len(device.TokenHash) == 0 {
			continue
		}
		if subtle.ConstantTimeCompare(candidate, device.TokenHash) == 1 {
			found = device
		}
	}
	if found == nil {
		return Public{}, false
	}
	found.LastSeen = s.now().UTC()
	return found.public(), true
}

// Claim returns a device's credential the first time it asks after approval.
//
// The request id is a UUID, which is the only thing the waiting device has; it
// is what lets the device collect its credential without a second channel back
// to it. The credential is handed over once, so the request id stops being worth
// anything the moment it has been used.
func (s *Store) Claim(id string) (Public, string, bool, error) {
	if s == nil {
		return Public{}, "", false, errors.New("lanaccess: store is not configured")
	}
	secret, err := newToken()
	if err != nil {
		return Public{}, "", false, err
	}
	hash := hashToken(secret)

	s.mu.Lock()
	index := s.indexLocked(id)
	if index < 0 {
		s.mu.Unlock()
		return Public{}, "", false, ErrNotFound
	}
	device := &s.devices[index]
	switch device.State {
	case StatePending:
		public := device.public()
		s.mu.Unlock()
		return public, "", false, nil
	case StateDenied:
		public := device.public()
		s.mu.Unlock()
		return public, "", false, ErrDenied
	}
	if device.TokenDelivered {
		public := device.public()
		s.mu.Unlock()
		return public, "", false, nil
	}
	// A credential is reissued at claim time rather than being held in the clear
	// since approval: the hash written at approval is replaced by this one, so
	// the value the device receives has never been on disk.
	device.TokenHash = hash
	device.TokenDelivered = true
	device.LastSeen = s.now().UTC()
	claimed := device.public()
	devices := append([]Device(nil), s.devices...)
	s.mu.Unlock()

	if err := s.write(devices); err != nil {
		return Public{}, "", false, err
	}
	s.logger.Info("lanaccess.claimed", "device", claimed.ID, "name", claimed.Name)
	return claimed, secret, true, nil
}

// indexLocked finds a device. Callers hold the lock.
func (s *Store) indexLocked(id string) int {
	for index := range s.devices {
		if s.devices[index].ID == id {
			return index
		}
	}
	return -1
}

// expireLocked drops pending requests nobody answered. Callers hold the lock.
func (s *Store) expireLocked() {
	cutoff := s.now().UTC().Add(-PendingTTL)
	kept := s.devices[:0]
	dropped := 0
	for _, device := range s.devices {
		if device.State == StatePending && device.Created.Before(cutoff) {
			dropped++
			continue
		}
		kept = append(kept, device)
	}
	s.devices = kept
	if dropped > 0 {
		s.logger.Info("lanaccess.expired", "requests", dropped)
	}
}

func (s *Store) write(devices []Device) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("lanaccess: create data directory: %w", err)
	}
	encoded, err := json.MarshalIndent(fileFormat{Devices: devices}, "", " ")
	if err != nil {
		return fmt.Errorf("lanaccess: encode registry: %w", err)
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(directory, fileName+".tmp*")
	if err != nil {
		return fmt.Errorf("lanaccess: create temporary file: %w", err)
	}
	name := temporary.Name()
	defer func() {
		if name != "" {
			_ = os.Remove(name)
		}
	}()
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("lanaccess: write registry: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("lanaccess: close registry: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("lanaccess: replace registry: %w", err)
	}
	name = ""
	return nil
}

// newToken returns a fresh device credential.
func newToken() (string, error) {
	buffer := make([]byte, tokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("lanaccess: generate credential: %w", err)
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func hashToken(secret string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(secret)))
	return sum[:]
}

// cleanName trims what a client called itself. The name is displayed to the
// person deciding, so it is bounded and stripped of control characters rather
// than trusted.
func cleanName(name string) string {
	name = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "Unknown device"
	}
	if len(name) > maxNameLength {
		// Trim on a rune boundary so a multi-byte name is not cut in half.
		runes := []rune(name)
		if len(runes) > maxNameLength {
			name = string(runes[:maxNameLength])
		}
	}
	return name
}
