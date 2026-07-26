// encrypt_secret takes a plaintext secret on stdin and prints the
// AES-256-GCM ciphertext (hex-encoded as nonce||ciphertext||tag) using
// the ARGENTUM_DSN_KEY env var as the master key.
//
// Used to seed company_llm_credentials.api_key_encrypted rows by hand
// until a CRUD HTTP endpoint exists. Example:
//
//	ENC=$(printf 'sk-tenant-anthropic-key' | go run ./scripts/encrypt_secret)
//	psql "$DB_URL" -c "INSERT INTO company_llm_credentials ..."
package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fauzanebd/argentum/internal/crypto"
)

func main() {
	hexKey := strings.TrimSpace(os.Getenv("ARGENTUM_DSN_KEY"))
	if hexKey == "" {
		fmt.Fprintln(os.Stderr, "ARGENTUM_DSN_KEY env var is required (64 hex chars)")
		os.Exit(1)
	}
	cipher, err := crypto.NewFromHex(hexKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cipher init: %v\n", err)
		os.Exit(1)
	}
	plain, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
		os.Exit(1)
	}
	blob, err := cipher.Encrypt(string(plain))
	if err != nil {
		fmt.Fprintf(os.Stderr, "encrypt: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(hex.EncodeToString(blob))
}
