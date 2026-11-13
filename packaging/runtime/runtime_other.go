//go:build !windows

package runtime

import "errors"

var errWindows = errors.New("Windows installation integration required")

func DefaultInstallDir() (string, error)         { return "", errWindows }
func ValidateInstallDir(string) error            { return errWindows }
func AcquireInstallation(string) (func(), error) { return nil, errWindows }
func CheckIdle(string) error                     { return errWindows }
func ValidateExisting(string) error              { return errWindows }
func RemoveOwnedRun(string) (bool, error)        { return false, errWindows }
func SyncInstalledVersion(string, string) error  { return errWindows }
