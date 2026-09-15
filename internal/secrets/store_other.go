//go:build !linux && !darwin

package secrets

import (
	"errors"
	"io"
	"os"
)

func readAgeStore(path string, limit int64) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store must be a regular non-symlink file"}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store cannot be read safely"}
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store exceeds the ciphertext limit"}
	}
	return data, true, nil
}
