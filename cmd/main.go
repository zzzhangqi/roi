package main

import (
	"fmt"
	"os"

	"github.com/rainbond/rainbond-offline-installer/cmd/roi"
)

func main() {
	if err := roi.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}