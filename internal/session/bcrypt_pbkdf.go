// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

// bcrypt_pbkdf(3) from OpenBSD — the KDF that protects passphrase-encrypted
// keys in OpenSSH's own private key format. golang.org/x/crypto keeps its
// copy in an internal package, so this is that implementation verbatim
// (x/crypto/ssh/internal/bcrypt_pbkdf), needed here only to read the one
// key type the library parses no further: DSA. See opensshdsa.go.
//
// See https://flak.tedunangst.com/post/bcrypt-pbkdf and
// https://cvsweb.openbsd.org/cgi-bin/cvsweb/src/lib/libutil/bcrypt_pbkdf.c.

import (
	"crypto/sha512"
	"errors"

	"golang.org/x/crypto/blowfish"
)

const bcryptBlockSize = 32

// bcryptPBKDF derives a key from the password, salt and rounds count,
// returning a []byte of length keyLen that can be used as cryptographic key.
func bcryptPBKDF(password, salt []byte, rounds, keyLen int) ([]byte, error) {
	if rounds < 1 {
		return nil, errors.New("bcrypt_pbkdf: number of rounds is too small")
	}
	if len(password) == 0 {
		return nil, errors.New("bcrypt_pbkdf: empty password")
	}
	if len(salt) == 0 || len(salt) > 1<<20 {
		return nil, errors.New("bcrypt_pbkdf: bad salt length")
	}
	if keyLen > 1024 {
		return nil, errors.New("bcrypt_pbkdf: keyLen is too large")
	}

	numBlocks := (keyLen + bcryptBlockSize - 1) / bcryptBlockSize
	key := make([]byte, numBlocks*bcryptBlockSize)

	h := sha512.New()
	h.Write(password)
	shapass := h.Sum(nil)

	shasalt := make([]byte, 0, sha512.Size)
	cnt, tmp := make([]byte, 4), make([]byte, bcryptBlockSize)
	for block := 1; block <= numBlocks; block++ {
		h.Reset()
		h.Write(salt)
		cnt[0] = byte(block >> 24)
		cnt[1] = byte(block >> 16)
		cnt[2] = byte(block >> 8)
		cnt[3] = byte(block)
		h.Write(cnt)
		bcryptHash(tmp, shapass, h.Sum(shasalt))

		out := make([]byte, bcryptBlockSize)
		copy(out, tmp)
		for i := 2; i <= rounds; i++ {
			h.Reset()
			h.Write(tmp)
			bcryptHash(tmp, shapass, h.Sum(shasalt))
			for j := 0; j < len(out); j++ {
				out[j] ^= tmp[j]
			}
		}

		for i, v := range out {
			key[i*numBlocks+(block-1)] = v
		}
	}
	return key[:keyLen], nil
}

var bcryptMagic = []byte("OxychromaticBlowfishSwatDynamite")

func bcryptHash(out, shapass, shasalt []byte) {
	c, err := blowfish.NewSaltedCipher(shapass, shasalt)
	if err != nil {
		panic(err)
	}
	for i := 0; i < 64; i++ {
		blowfish.ExpandKey(shasalt, c)
		blowfish.ExpandKey(shapass, c)
	}
	copy(out, bcryptMagic)
	for i := 0; i < 32; i += 8 {
		for j := 0; j < 64; j++ {
			c.Encrypt(out[i:i+8], out[i:i+8])
		}
	}
	// Swap bytes due to different endianness.
	for i := 0; i < 32; i += 4 {
		out[i+3], out[i+2], out[i+1], out[i] = out[i], out[i+1], out[i+2], out[i+3]
	}
}
