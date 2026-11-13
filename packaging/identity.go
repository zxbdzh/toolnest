package packaging

import (
	"encoding/json"
	"io"
)

// Identity is emitted by main's --toolnest-build-info before UI/network startup.
type Identity struct {
	Version         string `json:"version"`
	Repository      string `json:"repository"`
	PublicKey       string `json:"publicKey"`
	Channel         string `json:"channel"`
	RuntimeProtocol int    `json:"runtimeProtocol"`
}

// WriteBuildInfo must be wired by main only after the lifetime-lock contract
// is implemented. This read-only branch must not create files or registry keys.
func WriteBuildInfo(w io.Writer) error {
	return json.NewEncoder(w).Encode(Identity{Version(), Repository, UpdatePublicKey, BuildChannel, 1})
}
