package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/dsa" //nolint:staticcheck // reading keys users already have, not making new ones
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/ssh"
)

// OpenSSH's own private key format ("-----BEGIN OPENSSH PRIVATE KEY-----",
// the default ssh-keygen output since 7.8 in 2018) is what x/crypto parses
// for RSA, ECDSA and Ed25519 — and for DSA it stops at "unhandled key type".
// Jump servers that still only take ssh-dss are exactly where an id_dsa made
// by a 2018+ ssh-keygen turns up, so that one case is parsed here. The
// container layout and the passphrase handling mirror the library's
// parseOpenSSHPrivateKey so the errors mean the same thing to loadPrivateKey:
// *ssh.PassphraseMissingError when the key is encrypted and no passphrase
// was given, x509.IncorrectPasswordError when it was given and is wrong.
//
// Format (PROTOCOL.key in the OpenSSH source): magic "openssh-key-v1\0", then
// ciphername, kdfname, kdfoptions, number of keys (1), the public key blob,
// and the private block. The block, once decrypted, holds two equal check
// integers, the key type, the key fields (for ssh-dss: p, q, g, y, x), the
// comment and padding 1,2,3… up to the cipher block size.

const openSSHKeyMagic = "openssh-key-v1\x00"

func isOpenSSHDSAUnhandled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unhandled key type")
}

func parseOpenSSHDSA(pemBytes []byte, passphrase string) (ssh.Signer, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
		return nil, errors.New("ssh: not an OpenSSH private key")
	}
	raw := block.Bytes
	if len(raw) < len(openSSHKeyMagic) || string(raw[:len(openSSHKeyMagic)]) != openSSHKeyMagic {
		return nil, errors.New("ssh: invalid openssh private key format")
	}
	var w struct {
		CipherName   string
		KdfName      string
		KdfOpts      string
		NumKeys      uint32
		PubKey       []byte
		PrivKeyBlock []byte
		Rest         []byte `ssh:"rest"`
	}
	if err := ssh.Unmarshal(raw[len(openSSHKeyMagic):], &w); err != nil {
		return nil, err
	}
	if w.NumKeys != 1 {
		return nil, errors.New("ssh: multi-key files are not supported")
	}

	privBlock := w.PrivKeyBlock
	encrypted := w.CipherName != "none" || w.KdfName != "none"
	switch {
	case encrypted && passphrase == "":
		return nil, &ssh.PassphraseMissingError{}
	case encrypted:
		var err error
		privBlock, err = decryptOpenSSHBlock(w.CipherName, w.KdfName, w.KdfOpts, privBlock, []byte(passphrase))
		if err != nil {
			return nil, err
		}
	case w.KdfOpts != "":
		return nil, errors.New("ssh: invalid openssh private key")
	}

	var pk struct {
		Check1  uint32
		Check2  uint32
		Keytype string
		Rest    []byte `ssh:"rest"`
	}
	if err := ssh.Unmarshal(privBlock, &pk); err != nil || pk.Check1 != pk.Check2 {
		if encrypted {
			return nil, x509.IncorrectPasswordError
		}
		return nil, errors.New("ssh: malformed OpenSSH key")
	}
	if pk.Keytype != ssh.InsecureKeyAlgoDSA {
		return nil, fmt.Errorf("ssh: unhandled key type %q", pk.Keytype)
	}
	var k struct {
		P, Q, G *big.Int
		Y, X    *big.Int
		Comment string
		Pad     []byte `ssh:"rest"`
	}
	if err := ssh.Unmarshal(pk.Rest, &k); err != nil {
		return nil, err
	}
	for i, b := range k.Pad {
		if int(b) != i+1 {
			return nil, errors.New("ssh: padding not as expected")
		}
	}
	// Bound the parameters as the library does for public ssh-dss keys, so a
	// corrupt file cannot drag signing into huge-modulus arithmetic.
	if k.P.BitLen() > 4096 || k.Q.BitLen() > 512 {
		return nil, errors.New("ssh: dsa parameters too large")
	}
	if k.X.Sign() <= 0 || k.X.Cmp(k.Q) >= 0 {
		return nil, errors.New("ssh: invalid dsa private key")
	}
	priv := &dsa.PrivateKey{
		PublicKey: dsa.PublicKey{Parameters: dsa.Parameters{P: k.P, Q: k.Q, G: k.G}, Y: k.Y},
		X:         k.X,
	}
	return ssh.NewSignerFromKey(priv)
}

// decryptOpenSSHBlock is the library's passphraseProtectedOpenSSHKey: bcrypt
// KDF into a 32-byte AES key plus 16-byte IV, then aes256-ctr or aes256-cbc,
// which are the two ciphers ssh-keygen writes.
func decryptOpenSSHBlock(cipherName, kdfName, kdfOpts string, block, passphrase []byte) ([]byte, error) {
	if kdfName != "bcrypt" {
		return nil, fmt.Errorf("ssh: unknown KDF %q, only supports %q", kdfName, "bcrypt")
	}
	var opts struct {
		Salt   string
		Rounds uint32
	}
	if err := ssh.Unmarshal([]byte(kdfOpts), &opts); err != nil {
		return nil, err
	}
	const maxRounds = 1 << 11 // same cap as x/crypto: a hostile file must not pin the CPU
	if opts.Rounds > maxRounds {
		return nil, fmt.Errorf("ssh: bcrypt KDF rounds %d exceed maximum %d", opts.Rounds, maxRounds)
	}
	k, err := bcryptPBKDF(passphrase, []byte(opts.Salt), int(opts.Rounds), 32+16)
	if err != nil {
		return nil, err
	}
	key, iv := k[:32], k[32:]
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(block))
	switch cipherName {
	case "aes256-ctr":
		cipher.NewCTR(c, iv).XORKeyStream(out, block)
	case "aes256-cbc":
		if len(block)%c.BlockSize() != 0 {
			return nil, errors.New("ssh: invalid encrypted private key length, not a multiple of the block size")
		}
		cipher.NewCBCDecrypter(c, iv).CryptBlocks(out, block)
	default:
		return nil, fmt.Errorf("ssh: unknown cipher %q, only supports %q or %q", cipherName, "aes256-ctr", "aes256-cbc")
	}
	return out, nil
}
