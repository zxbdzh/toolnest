//go:build !windows

package update

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

func replaceFile(from, to string) error  { return os.Rename(from, to) }
func configureProcess(command *exec.Cmd) {}
func waitParent(context.Context, int) error {
	return errors.New("update process helper is supported only on Windows")
}

func lockState(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, ErrBusy
	}
	return func() { file.Close(); os.Remove(path) }, nil
}
