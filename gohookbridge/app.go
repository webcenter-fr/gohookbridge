package gohookbridge

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v2"
)

//go:embed templates/version
var Version []byte

//go:embed templates/zsh_completion.zsh
var zshCompletion []byte

//go:embed templates/bash_completion.bash
var bashCompletion []byte

const DefaultPublicHookURL = "https://hook.pipelinesascode.com/new"

func GetLogger(c *cli.Context) (*slog.Logger, bool, error) {
	nocolor := c.Bool("nocolor")
	w := os.Stdout
	var logger *slog.Logger
	switch c.String("output") {
	case "json":
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
		nocolor = true
	case "pretty":
		logger = slog.New(tint.NewHandler(w, &tint.Options{
			TimeFormat: time.RFC1123,
			NoColor:    !isatty.IsTerminal(w.Fd()),
		}))
	default:
		return nil, false, fmt.Errorf("invalid output format %s", c.String("output"))
	}
	return logger, nocolor, nil
}

func GetNewHookURL(targetURL string) (string, error) {
	client := &http.Client{}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create GET request: %w", err)
	}
	resp, err := client.Do(req) //nolint:gosec // user-configured URL
	if err != nil {
		return "", fmt.Errorf("failed to make GET request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func KeygenCommand() *cli.Command {
	return &cli.Command{
		Name:  "keygen",
		Usage: "Generate a client encryption keypair and print the public key",
		Action: func(c *cli.Context) error {
			publicKey, privateKey, err := GenerateKeyPair()
			if err != nil {
				return err
			}
			if err := SaveKeyPair(c.String("key-file"), publicKey, privateKey); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, EncodePublicKey(publicKey))
			return nil
		},
		Flags: KeygenFlags,
	}
}

func CompletionCommands() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "zsh",
			Usage: "generate zsh completion",
			Action: func(_ *cli.Context) error {
				os.Stdout.WriteString(string(zshCompletion))
				return nil
			},
		},
		{
			Name:  "bash",
			Usage: "generate bash completion",
			Action: func(_ *cli.Context) error {
				os.Stdout.WriteString(string(bashCompletion))
				return nil
			},
		},
		{
			Name:  "fish",
			Usage: "generate fish completion",
			Action: func(c *cli.Context) error {
				ret, err := c.App.ToFishCompletion()
				if err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout, ret)
				return nil
			},
		},
	}
}

func MakeApp(commands ...*cli.Command) *cli.App {
	return &cli.App{
		Name:  "gohookbridge",
		Usage: "Forward SMEE url from an external endpoint to a local service",
		UsageText: `Gohookbridge can help you reroute webhooks either from https://smee.io or its own server to a local service.
Where the server is the source of the webhook, and the client, which you run on your laptop or behind a
non-publicly accessible endpoint, forward those requests to your local service.`,
		EnableBashCompletion: true,
		Version:              strings.TrimSpace(string(Version)),
		Flags:                CommonFlags,
		Commands:             commands,
	}
}

func Run(args []string) error {
	return MakeApp().Run(args)
}
