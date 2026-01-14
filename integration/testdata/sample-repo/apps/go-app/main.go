package main

import (
	"fmt"
	"os"
)

func main() {
	repoRoot := os.Getenv("REPO_ROOT")
	bundleRoot := os.Getenv("BUNDLE_ROOT")
	goVar := os.Getenv("GO_VAR")

	fmt.Printf("REPO_ROOT=%s\n", repoRoot)
	fmt.Printf("BUNDLE_ROOT=%s\n", bundleRoot)
	fmt.Printf("GO_VAR=%s\n", goVar)
}
