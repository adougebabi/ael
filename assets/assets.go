package assets

import "embed"

//go:embed skills/*/* skills/*/references/* step-result.json assessment.json project-launcher.sh
var Files embed.FS
