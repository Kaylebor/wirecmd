//go:build linux || darwin

package secrets

import (
	"errors"
	"io"
	"os"
	"syscall"
)

func readAgeStore(path string, limit int64) ([]byte, bool, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, syscall.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store cannot be read safely"}
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store must be a regular non-symlink file"}
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store exceeds the ciphertext limit"}
	}
	return data, true, nil
}
