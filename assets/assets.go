package assets

import "embed"

//go:embed skills/*/* step-result.json project-launcher.sh
var Files embed.FS
