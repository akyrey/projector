package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/akyrey/projector/internal/web"
)

const (
	defaultServeAddr = "127.0.0.1:7070"

	// shutdownTimeout bounds stopping runs and closing connections on exit.
	shutdownTimeout = 10 * time.Second

	// readHeaderTimeout guards against slow clients holding connections.
	readHeaderTimeout = 10 * time.Second
)

func newServeCmd(d *deps) *cobra.Command {
	var addr string
	var open bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the local web dashboard",
		Long: `Start a web dashboard on this machine to see project status, run commands
with live output (including long-running ones like logs or dev servers), stop
them, and view project info items.

The server listens on a loopback address only. Open the printed link: it
carries a token, new on every start, in the URL fragment (never sent to any
server). The page keeps it for this origin until the server restarts. Runs
started from the dashboard stop when the server exits.

Examples:
  projector serve
  projector serve --open
  projector serve --addr 127.0.0.1:9000`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := web.NewToken()
			if err != nil {
				return err
			}

			ln, err := web.ListenLocal(addr)
			if err != nil {
				return err
			}

			dashboard := web.New(d.app, token)
			srv := &http.Server{Handler: dashboard, ReadHeaderTimeout: readHeaderTimeout}

			url := fmt.Sprintf("http://%s/#token=%s", ln.Addr(), token)
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "projector dashboard: %s\nPress Ctrl+C to stop.\n", url); err != nil {
				return err
			}
			if open {
				if err := openBrowser(url); err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "open browser: %v\n", err)
				}
			}

			serveErr := make(chan error, 1)
			go func() { serveErr <- srv.Serve(ln) }()

			var runErr error
			select {
			case err := <-serveErr:
				runErr = fmt.Errorf("serve: %w", err)
			case <-rootContext().Done():
			}

			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			// Stop runs and end streams first, so Shutdown has no open handlers to wait for.
			dashErr := dashboard.Shutdown(shutdownCtx)
			if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				dashErr = errors.Join(dashErr, fmt.Errorf("shutdown: %w", err))
			}
			return errors.Join(runErr, dashErr)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", defaultServeAddr, "loopback address to listen on")
	cmd.Flags().BoolVar(&open, "open", false, "open the dashboard in the default browser")
	return cmd
}

// openBrowser opens url with the platform's default handler.
func openBrowser(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }() // reap the launcher
	return nil
}
