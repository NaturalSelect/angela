//go:build !darwin

package notification

import (
	_ "embed"
)

//go:embed angela-icon-solo.png
var Icon []byte
