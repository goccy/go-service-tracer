package main

import (
	"log"

	servicetracer "github.com/goccy/go-service-tracer"
	"github.com/jessevdk/go-flags"
	"golang.org/x/xerrors"
)

func _main(args []string, opt *servicetracer.Option) error {
	cfg, err := servicetracer.LoadConfig(opt)
	if err != nil {
		return xerrors.Errorf("failed to load config: %w", err)
	}
	if err := servicetracer.New(cfg).Run(); err != nil {
		return xerrors.Errorf("failed to service trace: %w", err)
	}
	return nil
}

func main() {
	var opt servicetracer.Option
	parser := flags.NewParser(&opt, flags.Default)
	args, err := parser.Parse()
	if err != nil {
		return
	}
	if err := _main(args, &opt); err != nil {
		log.Fatalf("%+v", err)
	}
}
