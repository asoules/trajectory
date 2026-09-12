package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		writeJSON(os.Stderr, M{"error": err.Error()}, false)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output, diagnostics io.Writer) error {
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprint(output, cliHelp)
		return err
	}
	// Keep the original no-subcommand server invocation working.
	if len(args) == 0 || args[0] == "serve" || strings.HasPrefix(args[0], "-") {
		if len(args) > 0 && args[0] == "serve" {
			args = args[1:]
		}
		return serve(args, diagnostics)
	}
	return runCLI(args, input, output)
}

func serve(args []string, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("trajectory serve", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	port := flags.Int("port", 4318, "loopback HTTP port")
	path := flags.String("db", "", "Saved investigations database path (default: user data directory)")
	homeDefault := os.Getenv("CODEX_HOME")
	if homeDefault == "" {
		home, _ := os.UserHomeDir()
		homeDefault = filepath.Join(home, ".codex")
	}
	home := flags.String("home", homeDefault, "Codex data directory")
	demo := flags.Bool("demo", false, "use generated demo data only")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected server argument %q", flags.Arg(0))
	}
	dbPath, err := databasePath(*path, *demo)
	if err != nil {
		return err
	}
	db, err := openDB(dbPath)
	if err != nil {
		return err
	}
	defer db.close()
	store := newStore(db, *home)
	if *demo {
		if err := store.seedDemo(); err != nil {
			return err
		}
	}
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", *port), Handler: application(store), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if server.Shutdown(c) != nil {
			server.Close()
		}
	}()
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	logger := log.New(diagnostics, "", log.LstdFlags)
	logger.Printf("Trajectory → http://%s; SQLite: %s", listener.Addr(), dbPath)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	stop()
	<-done
	return nil
}
