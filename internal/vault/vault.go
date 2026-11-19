package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"gatekeep/internal/db"
	"golang.org/x/crypto/argon2"
)

const (
	kdfTime       = 3
	kdfMemory     = 64 * 1024
	kdfThreads    = 4
	minPassword   = 8
	minUnlock     = time.Minute
	maxUnlock     = 24 * time.Hour
	defaultUnlock = 15 * time.Minute
	unlockSetting = "unlock_duration_seconds"
)

var (
	ErrPasswordShort  = errors.New("password must be at least 8 characters")
	ErrPasswordMatch  = errors.New("passwords do not match")
	ErrWrongPassword  = errors.New("incorrect password")
	ErrAlreadySetup   = errors.New("password is already set")
	ErrNotSetup       = errors.New("password is not set")
	ErrLocked         = errors.New("locked")
	ErrUnlockDuration = errors.New("unlock duration must be from 1 minute to 24 hours")
)

type Session struct {
	Token     string
	ExpiresAt time.Time
}

type Status struct {
	State                 string
	ExpiresAt             time.Time
	UnlockDurationSeconds int
}

type Service struct {
	store *db.Store
	now   func() time.Time

	mu      sync.Mutex
	key     []byte
	token   string
	expires time.Time
}

func New(store *db.Store) *Service {
	return &Service{store: store, now: time.Now}
}

func (s *Service) Status(ctx context.Context, token string) (Status, error) {
	duration, err := s.unlockDuration(ctx)
	if err != nil {
		return Status{}, err
	}
	_, err = s.store.GetVault(ctx)
	configured := err == nil
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return Status{}, err
	}

	status := Status{
		State:                 "setup",
		UnlockDurationSeconds: int(duration / time.Second),
	}
	if configured {
		status.State = "locked"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if configured && token != "" && same(token, s.token) {
		if s.now().Before(s.expires) {
			status.State = "unlocked"
			status.ExpiresAt = s.expires
			return status, nil
		}
		s.clear()
	}
	return status, nil
}

func (s *Service) Setup(ctx context.Context, password, confirm string) (Session, error) {
	if utf8.RuneCountInString(password) < minPassword {
		return Session{}, ErrPasswordShort
	}
	if password != confirm {
		return Session{}, ErrPasswordMatch
	}
	if _, err := s.store.GetVault(ctx); err == nil {
		return Session{}, ErrAlreadySetup
	} else if !errors.Is(err, db.ErrNotFound) {
		return Session{}, err
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Session{}, err
	}
	key := derive(password, salt, kdfTime, kdfMemory, kdfThreads)
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		zero(key)
		return Session{}, err
	}
	verifier, err := encrypt(key, secret)
	zero(secret)
	if err != nil {
		zero(key)
		return Session{}, err
	}
	err = s.store.CreateVault(ctx, db.VaultRecord{
		Salt:     salt,
		Time:     kdfTime,
		Memory:   kdfMemory,
		Threads:  kdfThreads,
		Verifier: verifier,
	})
	if err != nil {
		zero(key)
		if errors.Is(err, db.ErrVaultExists) {
			return Session{}, ErrAlreadySetup
		}
		return Session{}, err
	}
	return s.begin(ctx, key)
}

func (s *Service) Unlock(ctx context.Context, password string) (Session, error) {
	record, err := s.store.GetVault(ctx)
	if errors.Is(err, db.ErrNotFound) {
		return Session{}, ErrNotSetup
	}
	if err != nil {
		return Session{}, err
	}
	if record.Time < 1 || record.Time > 10 || record.Memory < 8*1024 || record.Memory > 256*1024 || record.Threads < 1 || record.Threads > 16 {
		return Session{}, fmt.Errorf("stored password parameters are invalid")
	}
	key := derive(password, record.Salt, uint32(record.Time), uint32(record.Memory), uint8(record.Threads))
	opened, err := decrypt(key, record.Verifier)
	if err != nil || len(opened) != 32 {
		zero(key)
		zero(opened)
		return Session{}, ErrWrongPassword
	}
	zero(opened)
	return s.begin(ctx, key)
}

func (s *Service) Lock() {
	s.mu.Lock()
	s.clear()
	s.mu.Unlock()
}

// Expire locks the session once its deadline has passed.
func (s *Service) Expire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == "" || s.now().Before(s.expires) {
		return false
	}
	s.clear()
	return true
}

func (s *Service) Valid(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == "" || !same(token, s.token) || !s.now().Before(s.expires) {
		if token != "" && same(token, s.token) {
			s.clear()
		}
		return false
	}
	return true
}

func (s *Service) Seal(plaintext []byte) ([]byte, error) {
	key, err := s.keyCopy()
	if err != nil {
		return nil, err
	}
	defer zero(key)
	return encrypt(key, plaintext)
}

func (s *Service) Open(ciphertext []byte) ([]byte, error) {
	key, err := s.keyCopy()
	if err != nil {
		return nil, err
	}
	defer zero(key)
	opened, err := decrypt(key, ciphertext)
	if err != nil {
		return nil, ErrWrongPassword
	}
	return opened, nil
}

func (s *Service) SetUnlockDuration(ctx context.Context, duration time.Duration) error {
	if duration < minUnlock || duration > maxUnlock || duration%time.Second != 0 {
		return ErrUnlockDuration
	}
	_, err := s.store.PutSetting(ctx, unlockSetting, strconv.Itoa(int(duration/time.Second)))
	return err
}

func (s *Service) begin(ctx context.Context, key []byte) (Session, error) {
	duration, err := s.unlockDuration(ctx)
	if err != nil {
		zero(key)
		return Session{}, err
	}
	token, err := newToken()
	if err != nil {
		zero(key)
		return Session{}, err
	}
	expires := s.now().Add(duration)
	s.mu.Lock()
	s.clear()
	s.key = key
	s.token = token
	s.expires = expires
	s.mu.Unlock()
	return Session{Token: token, ExpiresAt: expires}, nil
}

func (s *Service) unlockDuration(ctx context.Context) (time.Duration, error) {
	setting, err := s.store.GetSetting(ctx, unlockSetting)
	if errors.Is(err, db.ErrNotFound) {
		return defaultUnlock, nil
	}
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.Atoi(setting.Value)
	if err != nil {
		return defaultUnlock, nil
	}
	duration := time.Duration(seconds) * time.Second
	if duration < minUnlock || duration > maxUnlock {
		return defaultUnlock, nil
	}
	return duration, nil
}

func (s *Service) keyCopy() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.key) == 0 || !s.now().Before(s.expires) {
		return nil, ErrLocked
	}
	return append([]byte(nil), s.key...), nil
}

func (s *Service) clear() {
	zero(s.key)
	s.key = nil
	s.token = ""
	s.expires = time.Time{}
}

func derive(password string, salt []byte, time, memory uint32, threads uint8) []byte {
	return argon2.IDKey([]byte(password), salt, time, memory, threads, 32)
}

func encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(key, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("ciphertext is too short")
	}
	nonce := sealed[:gcm.NonceSize()]
	return gcm.Open(nil, nonce, sealed[gcm.NonceSize():], nil)
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func same(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func zero(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
