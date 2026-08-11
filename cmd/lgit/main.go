package main

import (
	"os"

	"github.com/ErnieBernie10/lgit/internal/lgit"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		os.Stderr.WriteString("lgit: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit((lgit.App{Stdout: os.Stdout, Stderr: os.Stderr}).RunUX(cwd, os.Args[1:]))
}
