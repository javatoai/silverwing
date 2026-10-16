package desktop

import _ "embed"

// Embed the Silverwing brand icon so every native launch has the same icon,
// including development runs without an installer or an external asset path.
//
//go:embed assets/app-icon.png
var applicationIcon []byte
