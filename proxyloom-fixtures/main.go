// Command proxyloom-fixtures runs test-only isolation servers for compose.isolation.yaml.
// It is not a production entrypoint and must not be copied into API or Runner images.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Runarry/ProxyLoom/internal/isolation"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("isolation_fixture_failed: %v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "init-certs" {
		return runInitCerts(args[1:])
	}
	if len(args) > 1 {
		return errors.New("usage: proxyloom-fixtures target|a|b|init-certs [--out directory]")
	}
	role := os.Getenv("PROXYLOOM_ISOLATION_ROLE")
	if len(args) == 1 {
		role = args[0]
	}
	bind := os.Getenv("PROXYLOOM_ISOLATION_BIND")
	log := &isolation.Log{}
	switch role {
	case "target":
		target, err := isolation.StartHTTPTarget(bind, log)
		if err != nil {
			return err
		}
		defer target.Close()
	case "a", "b":
		cfg, err := loadProxyConfig(role, os.LookupEnv)
		if err != nil {
			return err
		}
		cfg.Log = log
		proxy, err := isolation.StartTrojan(cfg)
		if err != nil {
			return err
		}
		defer proxy.Close()
	default:
		return errors.New("usage: proxyloom-fixtures target|a|b|init-certs [--out directory]")
	}
	if eventsBind := os.Getenv("PROXYLOOM_ISOLATION_EVENTS_BIND"); eventsBind != "" {
		events, err := isolation.ListenEvents(eventsBind, log)
		if err != nil {
			return err
		}
		defer events.Close()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return nil
}
