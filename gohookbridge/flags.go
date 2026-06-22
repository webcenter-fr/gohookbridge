package gohookbridge

import (
	"github.com/urfave/cli/v2"
)

const (
	DefaultTimeout       = 5
	SmeeChannel          = "messages"
	DefaultLocalDebugURL = "http://localhost:8080"
	DefaultServerPort    = 8081
	DefaultServerAddress = "localhost"
)

var CommonFlags = []cli.Flag{
	&cli.StringFlag{
		Name:    "output",
		Usage:   `Output format, one of "json", "pretty"`,
		Value:   "pretty",
		Aliases: []string{"o"},
	},
	&cli.StringSliceFlag{
		Name:    "ignore-event",
		Aliases: []string{"I"},
		Usage:   "Ignore these events",
	},
	&cli.StringFlag{
		Name:    "saveDir",
		Usage:   "Save payloads to `DIR` populated with shell scripts to replay easily.",
		Aliases: []string{"s"},
		EnvVars: []string{"GOSMEE_SAVEDIR"},
	},
	&cli.IntFlag{
		Name:    "target-connection-timeout",
		Usage:   "How long to wait when forwarding the request to the service",
		EnvVars: []string{"GOSMEE_TARGET_TIMEOUT"},
		Value:   DefaultTimeout,
	},
	&cli.BoolFlag{
		Name:    "noReplay",
		Usage:   "Do not replay payloads",
		Aliases: []string{"n"},
		Value:   false,
	},
	&cli.BoolFlag{
		Name:    "nocolor",
		Usage:   "Disable color output, automatically disabled when non tty",
		EnvVars: []string{"NO_COLOR"},
	},
	&cli.BoolFlag{
		Name:  "insecure-skip-tls-verify",
		Value: false,
		Usage: "If true, the target server's certificate will not be checked for validity. This will make your HTTPS connections insecure",
	},
	&cli.StringFlag{
		Name:    "exec",
		Usage:   "Shell command to execute on each incoming webhook event. The JSON payload is available via $GOSMEE_PAYLOAD_FILE and headers via $GOSMEE_HEADERS_FILE (temporary files, cleaned up after execution). Security warning: do not use this with untrusted webhook sources without proper input validation",
		EnvVars: []string{"GOSMEE_EXEC"},
	},
	&cli.StringSliceFlag{
		Name:    "exec-on-events",
		Aliases: []string{"E"},
		Usage:   "Only run --exec on these event types (e.g., push, pull_request). If not set, --exec runs on all events",
	},
	&cli.StringSliceFlag{
		Name:    "exec-env-vars",
		Usage:   "Additional environment variable names to pass through to --exec commands. Can be specified multiple times",
		EnvVars: []string{"GOSMEE_EXEC_ENV_VARS"},
	},
}

var ReplayFlags = []cli.Flag{
	&cli.StringFlag{
		Name:     "github-token",
		Usage:    "GitHub token to use to replay payloads",
		Required: true,
		Aliases:  []string{"t"},
	},
	&cli.BoolFlag{
		Name:    "list-hooks",
		Usage:   "List hooks and its IDs on a repository",
		Aliases: []string{"L"},
	},
	&cli.BoolFlag{
		Name:    "list-deliveries",
		Usage:   "List deliveries from on hook ID",
		Aliases: []string{"D"},
	},
	&cli.StringFlag{
		Name:    "time-since",
		Aliases: []string{"T"},
		Usage:   "Replay events from this time",
	},
}

var KeygenFlags = []cli.Flag{
	&cli.StringFlag{
		Name:     "key-file",
		Usage:    "Path to write the client keypair JSON file",
		Required: true,
		EnvVars:  []string{"GOSMEE_ENCRYPTION_KEY_FILE"},
	},
}

var ClientFlags = []cli.Flag{
	&cli.BoolFlag{
		Name:    "new-url",
		Aliases: []string{"u"},
		Usage:   "Generate a new URL from https://hook.pipelinesascode.com",
		Value:   false,
	},
	&cli.BoolFlag{
		Name:  "httpie",
		Usage: "Use httpie instead of curl in replay scripts (requires httpie installed)",
		Value: false,
	},
	&cli.StringFlag{
		Name:    "channel",
		Aliases: []string{"c"},
		Usage:   "gohookbridge channel to listen, only useful when you are not use smee.io",
		Value:   SmeeChannel,
	},
	&cli.StringFlag{
		Name:  "local-debug-url",
		Usage: "Local URL when debugging the payloads",
		Value: DefaultLocalDebugURL,
	},
	&cli.IntFlag{
		Name:    "health-port",
		Usage:   "Port to expose health endpoint for Kubernetes liveness/readiness probes",
		Value:   0,
		EnvVars: []string{"GOSMEE_HEALTH_PORT"},
	},
	&cli.IntFlag{
		Name:    "sse-buffer-size",
		Usage:   "SSE client buffer size in bytes",
		Value:   1048576, // 1MB
		EnvVars: []string{"GOSMEE_SSE_BUFFER_SIZE"},
	},
	&cli.StringFlag{
		Name:    "encryption-key-file",
		Usage:   "Path to the client encryption keypair JSON file",
		EnvVars: []string{"GOSMEE_ENCRYPTION_KEY_FILE"},
	},
	&cli.StringFlag{
		Name:    "encryption-key",
		Usage:   "AES-256 encryption key (base64) for server-side encrypted channels",
		EnvVars: []string{"GOSMEE_ENCRYPTION_KEY"},
	},
	&cli.BoolFlag{
		Name:    "resume",
		Aliases: []string{"r"},
		Usage:   "Resume from last seen position on reconnect (uses client-id)",
		Value:   false,
	},
	&cli.StringFlag{
		Name:    "client-id",
		Usage:   "Client identifier for resume tracking (auto-generated if not set)",
		EnvVars: []string{"GOSMEE_CLIENT_ID"},
	},
	&cli.StringFlag{
		Name:    "token",
		Usage:   "Access token for channel authentication",
		EnvVars: []string{"GOSMEE_TOKEN"},
	},
}

var ProduceFlags = []cli.Flag{
	&cli.StringFlag{
		Name:    "pubkey",
		Usage:   "Channel public key (base64url) for E2E encryption",
		EnvVars: []string{"GOSMEE_ENCRYPTION_PUBLIC_KEY"},
	},
	&cli.StringFlag{
		Name:    "pubkey-file",
		Usage:   "Path to keypair JSON file (uses public_key field)",
		EnvVars: []string{"GOSMEE_ENCRYPTION_KEY_FILE"},
	},
	&cli.StringFlag{
		Name:    "token",
		Usage:   "Access token for channel authentication (sent as URL query parameter)",
		EnvVars: []string{"GOSMEE_TOKEN"},
	},
}

var ProxyFlags = []cli.Flag{
	&cli.StringFlag{
		Name:    "pubkey",
		Usage:   "Channel public key (base64url) for E2E encryption",
		EnvVars: []string{"GOSMEE_ENCRYPTION_PUBLIC_KEY"},
	},
	&cli.StringFlag{
		Name:    "pubkey-file",
		Usage:   "Path to keypair JSON file (uses public_key field)",
		EnvVars: []string{"GOSMEE_ENCRYPTION_KEY_FILE"},
	},
	&cli.StringFlag{
		Name:  "listen",
		Usage: "Address to listen on for incoming webhooks",
		Value: ":9090",
	},
	&cli.StringFlag{
		Name:     "target",
		Usage:    "Target gohookbridge server channel URL",
		Required: true,
	},
	&cli.StringFlag{
		Name:    "token",
		Usage:   "Access token for channel authentication (sent as URL query parameter)",
		EnvVars: []string{"GOSMEE_TOKEN"},
	},
}

var ServerFlags = []cli.Flag{
	&cli.StringFlag{
		Name:  "public-url",
		Usage: "Public URL to show to user, useful when you are behind a proxy.",
	},
	&cli.IntFlag{
		Name:    "port",
		Aliases: []string{"p"},
		Value:   DefaultServerPort,
		Usage:   "Port to listen on",
	},
	&cli.BoolFlag{
		Name:  "auto-cert",
		Value: false,
		Usage: "Automatically generate letsencrypt certs",
	},
	&cli.StringFlag{
		Name:    "address",
		Aliases: []string{"a"},
		Value:   DefaultServerAddress,
		Usage:   "Address to listen on",
	},
	&cli.StringFlag{
		Name:    "tls-cert",
		Usage:   "TLS certificate file",
		EnvVars: []string{"GOSMEE_TLS_CERT"},
	},
	&cli.StringFlag{
		Name:    "tls-key",
		Usage:   "TLS key file",
		EnvVars: []string{"GOSMEE_TLS_KEY"},
	},
	&cli.StringFlag{
		Name:    "raft-dir",
		Usage:   "Raft data directory (BoltDB stores)",
		Value:   "./raft-data",
		EnvVars: []string{"GOSMEE_RAFT_DIR"},
	},
	&cli.StringFlag{
		Name:    "raft-node-id",
		Usage:   "Unique Raft node ID",
		Value:   "node1",
		EnvVars: []string{"GOSMEE_RAFT_NODE_ID"},
	},
	&cli.StringFlag{
		Name:    "raft-bind-addr",
		Usage:   "Raft TCP bind address for inter-node communication",
		Value:   "127.0.0.1:6001",
		EnvVars: []string{"GOSMEE_RAFT_BIND_ADDR"},
	},
	&cli.StringSliceFlag{
		Name:    "raft-peers",
		Usage:   "Other Raft node IDs and addresses (node2=addr:port,node3=addr:port). Not needed for single-node",
		EnvVars: []string{"GOSMEE_RAFT_PEERS"},
	},
	&cli.StringFlag{
		Name:    "bootstrap-config-file",
		Usage:   "Path to bootstrap YAML/JSON config file (read once when FSM is empty)",
		EnvVars: []string{"GOSMEE_BOOTSTRAP_CONFIG_FILE"},
	},
	&cli.IntFlag{
		Name:    "nats-port",
		Usage:   "NATS embedded server port",
		Value:   4222,
		EnvVars: []string{"GOSMEE_NATS_PORT"},
	},
	&cli.IntFlag{
		Name:    "nats-cluster-port",
		Usage:   "NATS cluster route port for inter-node communication",
		Value:   6222,
		EnvVars: []string{"GOSMEE_NATS_CLUSTER_PORT"},
	},
	&cli.StringSliceFlag{
		Name:    "nats-routes",
		Usage:   "NATS cluster routes (nats://host:6222). Required for HA, same hosts as raft-peers",
		EnvVars: []string{"GOSMEE_NATS_ROUTES"},
	},
	&cli.IntFlag{
		Name:    "nats-buffer-size",
		Usage:   "Max number of webhook entries to keep in ring buffer",
		Value:   10000,
		EnvVars: []string{"GOSMEE_NATS_BUFFER_SIZE"},
	},
	&cli.BoolFlag{
		Name:  "dev-admin",
		Usage: "Auto-create an admin user on first boot when no users exist (development only). Password is written to raft-data/admin-password.txt.",
	},
	&cli.StringFlag{
		Name:  "dev-admin-password",
		Usage: "Password for the dev admin user. If empty, a random password is generated.",
	},
}
