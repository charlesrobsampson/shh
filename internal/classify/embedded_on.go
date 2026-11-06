//go:build embedmodel

package classify

import _ "embed"

// Built by `task model:build`; the file is not in git.
//
//go:embed assets/potion-base-8M-int8.bin
var embeddedAsset []byte

func embeddedModel() []byte { return embeddedAsset }

// HasEmbeddedModel reports whether this binary carries the embedding model (release builds, tag embedmodel).
func HasEmbeddedModel() bool { return true }
