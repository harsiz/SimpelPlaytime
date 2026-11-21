//go:build !windows

package main

import (
	"fmt"
	"log"
	"os"
)

func fatal(msg string) {
	log.Print(msg)
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
