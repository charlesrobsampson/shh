package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("shh", version)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: shh --version")
	os.Exit(2)
}
