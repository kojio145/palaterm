// Package vault encrypts the PalaTerm inventory at rest.
//
// The entire inventory (including device passwords and enable secrets) is
// serialized to JSON and sealed with AES-256-GCM. The key is derived from a
// user-supplied master password via scrypt, so nothing is ever written to disk
// in plaintext. There is no "plaintext mode": encryption is mandatory.
//
// File layout (all binary, little concern for portability since it is local):
//
//	magic   "PLTRMv1\n"          8 bytes
//	salt                        16 bytes   (scrypt)
//	nonce                       12 bytes   (GCM)
//	ciphertext || tag           N bytes
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/scrypt"

	"github.com/kojio145/palaterm/internal/model"
)

var magic = []byte("PLTRMv1\n")

const (
	saltLen  = 16
	nonceLen = 12
	keyLen   = 32 // AES-256

	// scrypt cost parameters (128 MiB memory, OWASP-recommended).
	scryptN = 1 << 17
	scryptR = 8
	scryptP = 1
)

// ErrWrongPassword is returned when decryption fails authentication, which for
// GCM is indistinguishable from a corrupted file or a wrong key.
var ErrWrongPassword = errors.New("wrong master password or corrupted vault")

func deriveKey(password string, salt []byte, n int) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, n, scryptR, scryptP, keyLen)
}

// Save encrypts inv with password and writes it atomically to path.
func Save(path, password string, inv *model.Inventory) error {
	plaintext, err := json.Marshal(inv)
	if err != nil {
		return fmt.Errorf("marshal inventory: %w", err)
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := deriveKey(password, salt, scryptN)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, magic)

	buf := make([]byte, 0, len(magic)+saltLen+nonceLen+len(ciphertext))
	buf = append(buf, magic...)
	buf = append(buf, salt...)
	buf = append(buf, nonce...)
	buf = append(buf, ciphertext...)

	return atomicWrite(path, buf)
}

// Load decrypts the vault at path using password.
func Load(path, password string) (*model.Inventory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < len(magic)+saltLen+nonceLen {
		return nil, ErrWrongPassword
	}
	if string(data[:len(magic)]) != string(magic) {
		return nil, fmt.Errorf("not a PalaTerm vault file")
	}
	off := len(magic)
	salt := data[off : off+saltLen]
	off += saltLen
	nonce := data[off : off+nonceLen]
	off += nonceLen
	ciphertext := data[off:]

	key, err := deriveKey(password, salt, scryptN)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, magic)
	if err != nil {
		return nil, ErrWrongPassword
	}

	var inv model.Inventory
	if err := json.Unmarshal(plaintext, &inv); err != nil {
		return nil, fmt.Errorf("corrupted inventory json: %w", err)
	}
	return &inv, nil
}

// Exists reports whether a vault file is present at path.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".palaterm-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmpName, 0o600)
	return os.Rename(tmpName, path)
}

var _ = io.Discard // reserved for future streaming API
