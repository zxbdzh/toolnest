package toolnest

import (
	"toolnest/internal/brand"
)

func trayPNG(failed bool) []byte {
	return brand.PNG(32, failed)
}
