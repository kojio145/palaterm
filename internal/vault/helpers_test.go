package vault

import (
	"bytes"
	"os"
)

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

func contains(haystack []byte, needle string) bool {
	return bytes.Contains(haystack, []byte(needle))
}
