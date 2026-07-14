package main

import (
	"fmt"
	"os"

	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "platform:", err)
		os.Exit(1)
	}
	fmt.Println(cfg.Summary())
}
