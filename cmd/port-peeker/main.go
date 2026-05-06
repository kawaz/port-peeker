// Command port-peeker is a one-binary HTTP service that answers LB health
// probes by inspecting the local host's TCP LISTEN state and (optionally)
// the name of the listening process via /proc.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	proxyproto "github.com/pires/go-proxyproto"

	"github.com/kawaz/port-peeker/internal/cache"
	"github.com/kawaz/port-peeker/internal/checker"
	"github.com/kawaz/port-peeker/internal/handler"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func printUsage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, "port-peeker - HTTP healthcheck agent that inspects host TCP LISTEN state via /proc")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  port-peeker [options]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, `  --listen ADDR         HTTP listen address (host:port) [default ":24365"]`)
	fmt.Fprintln(out, `  --cache-ttl DURATION  cache TTL for /check results; 0 disables [default 5s]`)
	fmt.Fprintln(out, "  --version             print version and exit")
	fmt.Fprintln(out, "  --help                show this help and exit")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "PROXY Protocol v1/v2 headers are auto-detected per connection,")
	fmt.Fprintln(out, "so the same listener works behind a PROXY-emitting LB and for direct probes.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Endpoints:")
	fmt.Fprintln(out, "  GET /check?port=N[&process=NAME]   200 if listening (and process matches when given)")
	fmt.Fprintln(out, "                                     503 otherwise, 400 on bad params")
	fmt.Fprintln(out, "  GET /healthz                       200 always (agent self-check)")
}

func main() {
	listen := flag.String("listen", ":24365", "HTTP listen address (host:port)")
	cacheTTL := flag.Duration("cache-ttl", 5*time.Second, "cache TTL for /check results; 0 disables")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = printUsage
	if len(os.Args) == 1 {
		flag.Usage()
		return
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	chk := checker.New()
	cch := cache.New[handler.Result](*cacheTTL)

	mux := http.NewServeMux()
	mux.Handle("/check", &handler.Check{Insp: chk, Cache: cch})
	mux.HandleFunc("/healthz", handler.Healthz)

	rawLn, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	// PROXY Protocol v1/v2 を自動検出 (USE policy: ヘッダ有→剥がす、無→素TCP)
	ln := &proxyproto.Listener{Listener: rawLn}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("port-peeker %s listening on %s (cache-ttl=%s)",
			version, *listen, *cacheTTL)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case s := <-sigCh:
		log.Printf("received %s, shutting down", s)
	case err := <-errCh:
		if err != nil {
			log.Fatalf("serve: %v", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
