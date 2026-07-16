package main

import (
	"fmt"
	"os"

	"github.com/miroslav-matejovsky/opdl/platform/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "platform:", err)
		os.Exit(1)
	}
}
