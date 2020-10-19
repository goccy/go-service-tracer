package main

import (
	"log"

	servicetracer "github.com/goccy/go-service-tracer"
	"golang.org/x/xerrors"
)

func _main() error {
	cfg, err := servicetracer.LoadConfig("trace.yaml")
	if err != nil {
		return xerrors.Errorf("failed to load config: %w", err)
	}
	if err := servicetracer.New(cfg).Run(); err != nil {
		return xerrors.Errorf("failed to service trace: %w", err)
	}
	return nil
}

func main() {
	if err := _main(); err != nil {
		log.Fatalf("%+v", err)
	}
}
