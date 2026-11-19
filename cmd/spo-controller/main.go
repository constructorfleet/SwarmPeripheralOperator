// Command spo-controller serves Swarm task device allocations to spo-runtime over mTLS.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/constructorfleet/SwarmPeripheralOperator/internal/controller"
	"github.com/constructorfleet/SwarmPeripheralOperator/internal/docker"
	"github.com/constructorfleet/SwarmPeripheralOperator/internal/version"
)

func main() {
	listen := flag.String("listen", ":7443", "listen address")
	cert := flag.String("tls-cert", "/run/secrets/spo-controller.crt", "server certificate")
	key := flag.String("tls-key", "/run/secrets/spo-controller.key", "server key")
	clientCA := flag.String("client-ca", "/run/secrets/spo-ca.crt", "CA for node client certificates")
	socket := flag.String("docker-socket", "/var/run/docker.sock", "manager dockerd socket")
	kinds := flag.String("kinds", "", "comma-separated Swarm generic resource kinds managed by SPO")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String("spo-controller"))
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("component", "spo-controller")
	if err := run(log, *listen, *cert, *key, *clientCA, *socket, *kinds); err != nil {
		log.Error("exit", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, listen, cert, key, clientCA, socket, kinds string) error {
	kindSet := map[string]bool{}
	for _, k := range strings.Split(kinds, ",") {
		if k = strings.TrimSpace(k); k != "" {
			kindSet[k] = true
		}
	}
	if len(kindSet) == 0 {
		return errors.New("--kinds is required")
	}
	tlsCfg, err := controller.ServerTLS(cert, key, clientCA)
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           (&controller.Server{Tasks: docker.New(socket), Kinds: kindSet, Log: log}).Handler(),
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServeTLS("", "") }()
	log.Info("listening", "addr", listen, "kinds", kinds, "version", version.Version)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
