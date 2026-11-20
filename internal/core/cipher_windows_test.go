//go:build windows

package core

import (
	"strings"
	"testing"
)

func TestSystemCipherRoundTripAndTampering(t *testing.T) {
	cipher := NewSystemCipher()
	raw := []byte(`{"password":"isolated-dpapi-test"}`)
	encrypted, err := cipher.Encrypt(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "isolated-dpapi-test") {
		t.Fatal("plaintext present in ciphertext")
	}
	plain, err := cipher.Decrypt(encrypted)
	if err != nil || string(plain) != string(raw) {
		t.Fatal("DPAPI round trip failed", err)
	}
	if _, err = cipher.Decrypt("electron-opaque-legacy-data"); err == nil {
		t.Fatal("legacy format accepted")
	}
	position := len(encrypted) / 2
	tampered := []byte(encrypted)
	if tampered[position] == 'A' {
		tampered[position] = 'B'
	} else {
		tampered[position] = 'A'
	}
	if _, err = cipher.Decrypt(string(tampered)); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}
