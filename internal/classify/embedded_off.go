//go:build !embedmodel

package classify

func embeddedModel() []byte { return nil }

// HasEmbeddedModel reports whether this binary carries the embedding model (release builds, tag embedmodel).
func HasEmbeddedModel() bool { return false }
