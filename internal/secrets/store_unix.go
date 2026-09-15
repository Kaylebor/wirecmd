//go:build linux || darwin

package secrets

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func readAgeStore(path string, limit int64) ([]byte, bool, error) {
	directory := filepath.Dir(path)
	root, err := os.OpenRoot(directory)
	if errors.Is(err, syscall.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store cannot be read safely"}
	}
	defer root.Close()
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store cannot be read safely"}
	}
	openedDirectoryInfo, err := root.Stat(".")
	if err != nil || directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() || !openedDirectoryInfo.IsDir() || !os.SameFile(directoryInfo, openedDirectoryInfo) {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store parent must remain a non-symlink directory"}
	}
	file, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store cannot be read safely"}
	}
	defer file.Close()
	pathInfo, err := root.Lstat(filepath.Base(path))
	if err != nil {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store cannot be read safely"}
	}
	info, err := file.Stat()
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !info.Mode().IsRegular() || !os.SameFile(pathInfo, info) {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store must be a regular non-symlink file"}
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, false, &Error{Code: CodeSecretStoreInvalid, Message: "age secret store exceeds the ciphertext limit"}
	}
	return data, true, nil
}
