// modelbuild converts a downloaded Model2Vec model into shh's quantized embedding asset and prints its sha256.
//
// Usage: modelbuild <model dir with model.safetensors and tokenizer.json> <output file> <model id>
package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charlesrobsampson/shh/internal/classify"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: modelbuild <model dir> <output file> <model id>")
		os.Exit(2)
	}
	dir, out, id := os.Args[1], os.Args[2], os.Args[3]
	safetensors, err := os.ReadFile(filepath.Join(dir, "model.safetensors"))
	check(err)
	tokenizer, err := os.ReadFile(filepath.Join(dir, "tokenizer.json"))
	check(err)
	asset, err := classify.BuildStaticModel(id, safetensors, tokenizer)
	check(err)
	check(os.WriteFile(out, asset, 0o644))
	fmt.Printf("%s  %d bytes  sha256 %x\n", out, len(asset), sha256.Sum256(asset))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "modelbuild:", err)
		os.Exit(1)
	}
}
