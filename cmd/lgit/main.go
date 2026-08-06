package main

import (
	"github.com/ErnieBernie10/lgit/internal/lgit"
	"os"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		os.Stderr.WriteString("lgit: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit((lgit.App{Stdout: os.Stdout, Stderr: os.Stderr}).Run(cwd, os.Args[1:]))
}
