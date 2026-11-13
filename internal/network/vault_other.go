//go:build !windows

package network

import "context"

type WindowsVault struct{ Namespace string }

func (WindowsVault) Read(context.Context, string) (Credentials, error) {
	return Credentials{}, ErrCredentials
}
func (WindowsVault) Write(context.Context, string, Credentials) error { return ErrCredentials }
func (WindowsVault) Delete(context.Context, string) error             { return ErrCredentials }
