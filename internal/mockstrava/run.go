package mockstrava

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// RunConfig is what cmd/mock-strava passes from its flags.
type RunConfig struct {
	// Listen is the address to bind ("127.0.0.1:0" picks a free port).
	Listen string
	// AddrFile, when set, receives the bound base URL.
	AddrFile string
	// Stdout gets the "listening on …" line; nil means os.Stdout.
	Stdout io.Writer
	// Ready, when set, is called with the base URL once the listener is up.
	Ready func(baseURL string)
}

// Run serves the mock until ctx ends.
func Run(ctx context.Context, cfg RunConfig, opts Options) error {
	if cfg.Listen == "" {
		return errors.New("listen address required")
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Listen, err)
	}
	base := "http://" + ln.Addr().String()
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	_, _ = fmt.Fprintf(cfg.Stdout, "listening on %s\n", base)
	if cfg.AddrFile != "" {
		if err := os.WriteFile(cfg.AddrFile, []byte(base+"\n"), 0o600); err != nil {
			_ = ln.Close()
			return fmt.Errorf("write addr file: %w", err)
		}
	}
	if cfg.Ready != nil {
		cfg.Ready(base)
	}

	srv := &http.Server{Handler: New(opts).Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errc:
		return err
	}
}
