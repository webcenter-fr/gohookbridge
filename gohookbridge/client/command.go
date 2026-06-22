package client

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
	"github.com/urfave/cli/v2"
	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:      "client",
		UsageText: "gohookbridge client [command options] SMEE_URL LOCAL_SERVICE_URL",
		Usage:     "Make a client from the relay server to your local service",
		Action:    clientAction,
		Flags:     append(gohookbridge.CommonFlags, gohookbridge.ClientFlags...),
	}
}

func ReplayCommand() *cli.Command {
	return &cli.Command{
		Name:  "replay",
		Usage: "Replay payloads from GitHub",
		Action: func(c *cli.Context) error {
			return replay(c)
		},
		Flags: append(gohookbridge.CommonFlags, gohookbridge.ReplayFlags...),
	}
}

func clientAction(c *cli.Context) error {
	logger, nocolor, err := gohookbridge.GetLogger(c)
	if err != nil {
		return err
	}

	if c.Bool("new-url") {
		url, err := gohookbridge.GetNewHookURL(gohookbridge.DefaultPublicHookURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return cli.Exit("", 1)
		}
		fmt.Fprintln(os.Stdout, strings.TrimSpace(url))
		return cli.Exit("", 0)
	}

	var smeeURL, targetURL string
	noReplay := c.Bool("noReplay")
	switch {
	case os.Getenv("GOSMEE_URL") != "" && os.Getenv("GOSMEE_TARGET_URL") != "":
		smeeURL = os.Getenv("GOSMEE_URL")
		targetURL = os.Getenv("GOSMEE_TARGET_URL")
	case c.String("exec") != "" && c.NArg() == 1:
		smeeURL = c.Args().Get(0)
		noReplay = true
	default:
		if c.NArg() != 2 {
			return fmt.Errorf("need at least a smeeURL and a targetURL as arguments, ie: gohookbridge client https://server.smee.url/aBcdeFghijklmn http://localhost:8080")
		}
		smeeURL = c.Args().Get(0)
		targetURL = c.Args().Get(1)
	}
	if _, err := url.Parse(smeeURL); err != nil {
		return fmt.Errorf("smeeURL %s is not a valid url %w", smeeURL, err)
	}
	if targetURL != "" {
		if _, err := url.Parse(targetURL); err != nil {
			return fmt.Errorf("target url %s is not a valid url %w", targetURL, err)
		}
	}
	decorate := true
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		ansi.DisableColors(true)
		decorate = false
	}
	if nocolor {
		ansi.DisableColors(true)
		decorate = false
	}
	localDebugURL := c.String("local-debug-url")
	if localDebugURL == "" {
		localDebugURL = gohookbridge.DefaultLocalDebugURL
	}

	healthPort := c.Int("health-port")
	if healthPort > 0 {
		serveHealthEndpoint(healthPort, logger, decorate)
	}

	cfg := goSmee{
		replayDataOpts: &replayDataOpts{
			smeeURL:           smeeURL,
			targetURL:         targetURL,
			localDebugURL:     localDebugURL,
			saveDir:           c.String("saveDir"),
			noReplay:          noReplay,
			decorate:          decorate,
			ignoreEvents:      c.StringSlice("ignore-event"),
			targetCnxTimeout:  c.Int("target-connection-timeout"),
			insecureTLSVerify: c.Bool("insecure-skip-tls-verify"),
			useHttpie:         c.Bool("httpie"),
			sseBufferSize:     c.Int("sse-buffer-size"),
			execCommand:       c.String("exec"),
			execOnEvents:      c.StringSlice("exec-on-events"),
			execEnvVars:       c.StringSlice("exec-env-vars"),
			encryptionKeyFile: c.String("encryption-key-file"),
			encryptionKey:     c.String("encryption-key"),
			resume:            c.Bool("resume"),
			clientID:          c.String("client-id"),
			token:             c.String("token"),
		},
		logger:  logger,
		channel: c.String("channel"),
	}
	return cfg.clientSetup()
}
