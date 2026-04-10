package main

import (
	"fmt"

	"github.com/Tethik/massrepo/internal/shared"
)

var (
	version string
	commit  string
	build   string
)

func main() {
	fmt.Println("Hello world")
	fmt.Println(version, commit, build)
	fmt.Println(shared.SomeLibraryFunction())
}