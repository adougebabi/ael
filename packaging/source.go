// Package packaging holds the publisher trust configuration shipped with AEL.
package packaging

import _ "embed"

//go:embed client-source.github.json
var OfficialClientSource []byte
