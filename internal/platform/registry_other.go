//go:build !windows

package platform

import "context"

func NewRegistry(options Options) (*Registry, error) { return nil, ErrUnsupported }
func HandleHelper(ctx context.Context, args []string) (bool, int, error) {
	if len(args) > 0 && args[0] == "--toolnest-path-helper" {
		return true, 2, ErrUnsupported
	}
	return false, 0, nil
}
