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
	"os"
	"path/filepath"
	"strings"
	"time"

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

func deriveKey(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, keyLen)
}

// Writer is the version of the PalaTerm saving (set once at start-up),
// recorded in the vault as Inventory.SavedBy.
var Writer string

// StampVersion is the format Save records (model.InventoryVersion; tests
// raise it to make a vault "from a newer PalaTerm").
var StampVersion = model.InventoryVersion

// Save encrypts inv with password and writes it atomically to path. The vault
// is stamped with this build's format (StampVersion) and version (Writer).
func Save(path, password string, inv *model.Inventory) error {
	inv.Version = StampVersion
	if Writer != "" {
		inv.SavedBy = Writer
	}
	plaintext, err := json.Marshal(inv)
	if err != nil {
		return fmt.Errorf("marshal inventory: %w", err)
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := deriveKey(password, salt)
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

	key, err := deriveKey(password, salt)
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
	// On disk before the rename: without it a power cut can leave the
	// renamed file empty, and the vault holds every credential there is.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmpName, 0o600)
	// One generation back, for the day the current file is damaged anyway
	// (disk error, an antivirus quarantine): <vault>.bak, same encryption.
	// It keeps the time that generation was saved, which the offer to
	// restore it names.
	if old, err := os.ReadFile(path); err == nil && len(old) > 0 {
		if writeSynced(BackupPath(path), old) == nil {
			if st, err := os.Stat(path); err == nil {
				_ = os.Chtimes(BackupPath(path), st.ModTime(), st.ModTime())
			}
		}
	}
	// A terminal window reading the vault at that moment makes Windows
	// refuse the replace (sharing violation); it lets go within moments.
	for i := 0; i < 10; i++ {
		if err = os.Rename(tmpName, path); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}

// BackupPath is where Save keeps the previous generation of the vault.
func BackupPath(path string) string { return path + ".bak" }

// LoadBackup decrypts the previous generation (BackupPath) with password and
// returns it with the time it was saved. The lock screen offers it when the
// vault itself will not open with a password the backup accepts — a damaged
// file rather than a mistyped password.
func LoadBackup(path, password string) (*model.Inventory, time.Time, error) {
	st, err := os.Stat(BackupPath(path))
	if err != nil {
		return nil, time.Time{}, err
	}
	inv, err := Load(BackupPath(path), password)
	if err != nil {
		return nil, time.Time{}, err
	}
	return inv, st.ModTime(), nil
}

// RestoreBackup puts the previous generation back in place of the vault. The
// file it replaces is kept beside it as <path>.broken-<time>, whose name is
// returned ("" when there was no file to keep). The backup stays as it is.
func RestoreBackup(path string, now time.Time) (string, error) {
	data, err := os.ReadFile(BackupPath(path))
	if err != nil {
		return "", err
	}
	if len(data) < len(magic) || string(data[:len(magic)]) != string(magic) {
		return "", fmt.Errorf("not a PalaTerm vault file")
	}
	kept := ""
	if _, err := os.Stat(path); err == nil {
		stamp := now.Format("20060102-150405")
		kept = path + ".broken-" + stamp
		for i := 2; Exists(kept); i++ {
			if i > 99 {
				return "", fmt.Errorf("cannot name the damaged vault's copy")
			}
			kept = fmt.Sprintf("%s.broken-%s_%d", path, stamp, i)
		}
		if err := os.Rename(path, kept); err != nil {
			return "", err
		}
	}
	if err := writeSynced(path, data); err != nil {
		// Put the original back rather than leave no vault at all.
		if kept != "" {
			_ = os.Remove(path)
			_ = os.Rename(kept, path)
		}
		return "", err
	}
	return kept, nil
}

// RemoveBackups deletes every other copy of the vault beside it: the backup
// (<path>.bak), the damaged files RestoreBackup set aside
// (<path>.broken-<time>, still readable with the password of their day) and
// any temporary file a failed save left. Called when the master password
// changes or the vault is reset, so no file encrypted under the old password
// stays behind. The directory is listed rather than globbed: a folder name
// with [ ] in it is no pattern.
func RemoveBackups(path string) {
	_ = os.Remove(path + ".bak")
	dir, base := filepath.Dir(path), filepath.Base(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, base+".broken-") ||
			(strings.HasPrefix(name, ".palaterm-") && strings.HasSuffix(name, ".tmp")) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// writeSynced writes data to path and flushes it to disk.
func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
