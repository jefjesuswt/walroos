package secretbox_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jefjesuswt/walroos/internal/secretbox"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	box, err := secretbox.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "s3cret") {
		t.Fatal("ciphertext contains the plaintext")
	}
	if got, err := box.Open(sealed); err != nil || got != "s3cret" {
		t.Fatalf("open: %q, %v", got, err)
	}

	// The same file gives the same key back; a different key can't read it.
	again, err := secretbox.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := again.Open(sealed); err != nil || got != "s3cret" {
		t.Fatalf("reloaded box: %q, %v", got, err)
	}
	other, err := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "other.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("a different key decrypted the value")
	}
}

func TestSealIsRandomized(t *testing.T) {
	box, err := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := box.Seal("same")
	b, _ := box.Seal("same")
	if a == b {
		t.Fatal("two seals of the same value are identical: nonce isn't random")
	}
}
