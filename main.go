package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"

	"github.com/cli/go-gh/v2/pkg/browser"
	"github.com/reneruck/gh-design-review/internal/github"
	"github.com/reneruck/gh-design-review/internal/server"
	"github.com/reneruck/gh-design-review/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gh design-review:", err)
		os.Exit(1)
	}
}

func run() error {
	f := flag.NewFlagSet("design-review", flag.ExitOnError)
	f.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: gh design-review [<number> | <url> | <branch>] [flags]")
		f.PrintDefaults()
	}
	var repo, glob string
	f.StringVar(&repo, "R", "", "Repository `owner/repo` (default: current directory's repo)")
	f.StringVar(&repo, "repo", "", "Same as -R")
	f.StringVar(&glob, "glob", "docs/designs/**/*.md", "Design doc glob")
	port := f.Int("port", 0, "Port (default: random free port)")
	noBrowser := f.Bool("no-browser", false, "Print the URL instead of opening a browser")

	// Allow flags before and after the PR argument.
	var arg string
	f.Parse(os.Args[1:])
	for f.NArg() > 0 {
		if arg != "" {
			return fmt.Errorf("unexpected argument %q", f.Arg(0))
		}
		arg = f.Arg(0)
		f.Parse(f.Args()[1:])
	}

	re, err := server.GlobRegexp(glob)
	if err != nil {
		return err
	}
	pr, err := github.ResolvePR(arg, repo)
	if err != nil {
		return err
	}
	client, err := github.New(pr)
	if err != nil {
		return err
	}

	// Dev loop: DR_WEB_DIR=web/dist serves the frontend from disk instead of the embedded copy.
	var assets fs.FS
	if dir := os.Getenv("DR_WEB_DIR"); dir != "" {
		assets = os.DirFS(dir)
	} else if assets, err = fs.Sub(web.Dist, "dist"); err != nil {
		return err
	}

	tok := make([]byte, 32)
	rand.Read(tok)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	srv := &server.Server{GH: client, PR: pr, Glob: re, Token: hex.EncodeToString(tok),
		Port: ln.Addr().(*net.TCPAddr).Port, Assets: assets}

	_, docs, err := srv.Snapshot()
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return fmt.Errorf("PR #%d changes no file matching %s", pr.Number, glob)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", srv.Port, srv.Token)
	fmt.Printf("Reviewing %s (%d design doc(s))\n%s\nCtrl+C to stop.\n", pr.URL, len(docs), url)
	if !*noBrowser {
		if err := browser.New("", os.Stdout, os.Stderr).Browse(url); err != nil {
			fmt.Fprintln(os.Stderr, "could not open browser:", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	hs := &http.Server{Handler: srv.Handler()}
	go func() {
		<-ctx.Done()
		hs.Shutdown(context.Background())
	}()
	if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
