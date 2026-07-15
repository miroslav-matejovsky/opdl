package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/httpapi"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "platform:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("platform", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "path to the platform JSON configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	fmt.Println(cfg.Summary())
	descriptor := cfg.Descriptor()
	registrations, err := registration.NewService(
		registration.Location{Machine: descriptor.Machine, IP: descriptor.IP},
		registration.SingleInstanceCoordinator{},
	)
	if err != nil {
		return fmt.Errorf("registration service: %w", err)
	}

	addr := cfg.Address()
	fmt.Printf("platform: listening on %s\n", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewHandler(registrations),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.ListenAndServe()
}
