// Command pat-sign generates the release signing key and signs release assets.
//
// It is run by hand, and by `build.ps1 -Release`. It is not part of the monitor
// and the monitor never calls it: the private key exists on one machine, and
// this is the only thing that touches it.
//
//	pat-sign -generate -key release.key    # once, ever
//	pat-sign -key release.key file.zip     # writes file.zip.sig
//	pat-sign -verify file.zip              # checks it against the shipped key
//
// **Why a signature at all, when the download is over HTTPS.** TLS proves the
// bytes came from GitHub unaltered. It does not prove they are ours: whoever
// takes the account publishes a release and every installation fetches it. A
// checksum published beside the archive does not close that, because the same
// account publishes the checksum. What does is a key that has never been near
// GitHub — which is this one, and which is why it lives on a disk and not in a
// secret store belonging to the thing it is defending against.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"patmonitor/internal/update"
)

func main() {
	var (
		generate = flag.Bool("generate", false, "make a new key pair and print the constant to paste")
		verify   = flag.Bool("verify", false, "check a file against the key compiled into this binary")
		keyPath  = flag.String("key", "", "path of the private key")
	)
	flag.Parse()

	if err := run(*generate, *verify, *keyPath, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "pat-sign:", err)
		os.Exit(1)
	}
}

func run(generate, verify bool, keyPath string, args []string) error {
	switch {
	case generate:
		return makeKey(keyPath)
	case verify:
		if len(args) != 1 {
			return fmt.Errorf("-verify takes one file")
		}
		return verifyFile(args[0])
	default:
		if len(args) != 1 {
			return fmt.Errorf("give one file to sign")
		}
		return signFile(keyPath, args[0])
	}
}

// makeKey writes the private half and prints the public one as Go source.
//
// **It refuses to overwrite.** A second `-generate` over an existing key would
// silently strand every installation carrying the first one, and it would do so
// at the exact moment somebody is in a hurry — which is when a command gets run
// twice. The refusal is the create itself, which fails if the file exists, so
// nothing can slip in between a check and a write. The printed constant is
// meant to be pasted into internal/update/pubkey.go: two halves that are
// replaced together or not at all.
//
// **The key is plain hex, and the 0600 below is not what protects it.** On
// Windows a file mode is the read-only attribute and nothing else; what keeps
// other accounts away is the folder's ACL. So the key belongs under the
// profile, or on removable or encrypted media — never in a folder at the root
// of a drive, where every account on the machine may read it.
func makeKey(path string) error {
	if path == "" {
		return fmt.Errorf("-generate needs -key, to say where to write it")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists: a new key strands every installation carrying the old one", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(hex.EncodeToString(priv) + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("var PublicKey = [32]byte{")
	for i, by := range pub {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "0x%02x", by)
	}
	b.WriteString("}")

	fmt.Printf("private key written to %s — back it up, and keep it out of the repository\n", path)
	fmt.Printf("paste into internal/update/pubkey.go:\n\n%s\n", b.String())
	return nil
}

func signFile(keyPath, file string) error {
	if keyPath == "" {
		return fmt.Errorf("give -key")
	}
	priv, err := loadKey(keyPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(priv, data)

	out := file + ".sig"
	if err := os.WriteFile(out, []byte(hex.EncodeToString(sig)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s signed -> %s\n", filepath.Base(file), filepath.Base(out))
	return nil
}

// verifyFile checks a file the way an installation would.
//
// **It uses the key compiled into this binary**, not one passed on the command
// line, and that is the point of it: a check against a key handed over at the
// same time as the file proves only that the two match each other. This asks
// the question the monitor will ask — does this archive carry a signature made
// by the key that shipped?
func verifyFile(file string) error {
	if !update.IsSigned() {
		return fmt.Errorf("no key has been generated yet: internal/update/pubkey.go is still the placeholder")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(file + ".sig")
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return fmt.Errorf("the signature is not readable: %w", err)
	}
	if !ed25519.Verify(update.PublicKey[:], data, sig) {
		return fmt.Errorf("%s does not carry a signature from the shipped key", filepath.Base(file))
	}
	fmt.Printf("%s verifies against the shipped key\n", filepath.Base(file))
	return nil
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("%s is not a key this tool wrote: %w", path, err)
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is %d bytes, not %d", path, len(b), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(b), nil
}
