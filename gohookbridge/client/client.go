package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	_ "embed"

	"github.com/mgutz/ansi"
	"github.com/mitchellh/mapstructure"
	"github.com/r3labs/sse/v2"
	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/cenkalti/backoff.v1"
)

var Version string

//go:embed templates/replay_script.tmpl.bash
var shellScriptTmpl []byte

//go:embed templates/replay_script.tmpl.httpie.bash
var shellScriptHttpieTmpl []byte

var pmEventRe = regexp.MustCompile(`(\w+|\d+|_|-|:)`)

const (
	defaultTimeout       = 5
	smeeChannel          = "messages"
	defaultLocalDebugURL = "http://localhost:8080"
	tsFormat             = "2006-01-02T15.04.01.000"
)

type goSmee struct {
	replayDataOpts *replayDataOpts
	channel        string
	logger         *slog.Logger
}

type payloadMsg struct {
	headers     map[string]string
	body        []byte
	timestamp   string
	contentType string
	eventType   string
	eventID     string
}

type messageBody struct {
	Body  json.RawMessage `json:"body"`
	BodyB string          `json:"bodyB"`
}

func title(source string) string {
	return cases.Title(language.Und, cases.NoLower).String(source)
}

func getOrCreateClientID() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".gohookbridge")
	_ = os.MkdirAll(dir, 0700)

	idFile := filepath.Join(dir, "client-id")
	if data, err := os.ReadFile(idFile); err == nil {
		return strings.TrimSpace(string(data))
	}

	id := gohookbridge.GenerateUUID()
	_ = os.WriteFile(idFile, []byte(id), 0600)
	return id
}

func (c goSmee) parse(now time.Time, data []byte) (payloadMsg, error) {
	dt := now
	pm := payloadMsg{
		headers: make(map[string]string),
	}
	pm.eventID = ""
	var message any
	_ = json.Unmarshal(data, &message)
	var payload map[string]any
	err := mapstructure.Decode(message, &payload)
	if err != nil {
		return payloadMsg{}, err
	}

	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	c.logger.DebugContext(context.Background(), fmt.Sprintf("Received payload with keys: %v", keys))

	for payloadKey, payloadValue := range payload {
		switch payloadKey {
		case "x-github-event", "x-gitlab-event", "x-event-key":
			if pv, ok := payloadValue.(string); ok {
				pm.headers[title(payloadKey)] = pv
				replace := strings.NewReplacer(":", "-", " ", "_", "/", "_")
				pv = replace.Replace(strings.ToLower(pv))
				pv = pmEventRe.FindString(pv)
				pm.eventType = pv
			}
		case "x-github-delivery":
			if pv, ok := payloadValue.(string); ok {
				pm.headers[title(payloadKey)] = pv
				pm.eventID = pv
			}
		case "bodyB":
			mb := &messageBody{}
			if err := json.NewDecoder(strings.NewReader(string(data))).Decode(mb); err != nil {
				return pm, err
			}
			decoded, err := base64.StdEncoding.DecodeString(string(mb.BodyB))
			if err != nil {
				return pm, err
			}
			pm.body = decoded
		case "body":
			mb := &messageBody{}
			if err := json.NewDecoder(strings.NewReader(string(data))).Decode(mb); err != nil {
				return pm, err
			}
			pm.body = mb.Body
		case "content-type":
			if pv, ok := payloadValue.(string); ok {
				pm.contentType = pv
				if _, exists := pm.headers["Content-Type"]; !exists {
					pm.headers["Content-Type"] = pv
				}
			}
		case "timestamp":
			if pv, ok := payloadValue.(string); ok {
				tsInt, err := strconv.ParseInt(pv, 10, 64)
				if err != nil {
					s := fmt.Sprintf("%s cannot convert timestamp to int64, %s", ansi.Color("ERROR", "red+b"), err.Error())
					c.logger.ErrorContext(context.Background(), s)
				} else {
					dt = time.Unix(tsInt/int64(1000), (tsInt%int64(1000))*int64(1000000)).UTC()
				}
			}
		default:
			if strings.HasPrefix(payloadKey, "x-") || payloadKey == "user-agent" {
				if pv, ok := payloadValue.(string); ok {
					if strings.ToLower(payloadKey) == "x-forwarded-for" {
						pv = strings.Split(pv, ":")[0]
					}
					pm.headers[title(payloadKey)] = pv
				}
			} else if payloadKey != "bodyB" && payloadKey != "body" && payloadValue != nil {
				if pv, ok := payloadValue.(string); ok {
					pm.headers[title(payloadKey)] = pv
				}
			}
		}
	}

	pm.timestamp = dt.Format(tsFormat)

	if len(pm.headers) == 0 && pm.contentType != "" {
		pm.headers["Content-Type"] = pm.contentType
	}

	if len(c.replayDataOpts.ignoreEvents) > 0 &&
		pm.eventType != "" &&
		slices.Contains(c.replayDataOpts.ignoreEvents, pm.eventType) {
		s := fmt.Sprintf("%sskipping event %s as requested", emoji("!", "blue+b", c.replayDataOpts.decorate), pm.eventType)
		c.logger.InfoContext(context.Background(), s)
		return pm, nil
	}

	if len(pm.headers) == 0 && len(pm.body) == 0 {
		return pm, fmt.Errorf("parsed message has no headers")
	}

	return pm, nil
}

func emoji(emoji, color string, decorate bool) string {
	if !decorate {
		return ""
	}
	return ansi.Color(emoji, color) + " "
}

func buildHeaders(headers map[string]string) string {
	var b strings.Builder
	for k, v := range headers {
		fmt.Fprintf(&b, "%s=%s ", k, v)
	}
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func buildCurlHeaders(headers map[string]string) string {
	var b strings.Builder
	for k, v := range headers {
		fmt.Fprintf(&b, "-H %s ", shellQuote(fmt.Sprintf("%s: %s", k, v)))
	}
	return b.String()
}

func saveData(rd *replayDataOpts, logger *slog.Logger, pm payloadMsg) error {
	if _, err := os.Stat(rd.saveDir); os.IsNotExist(err) {
		if err := os.MkdirAll(rd.saveDir, 0o755); err != nil {
			return err
		}
	}

	fbasepath := pm.timestamp
	if pm.eventType != "" {
		fbasepath = fmt.Sprintf("%s-%s", pm.eventType, pm.timestamp)
	}

	jsonfile := fmt.Sprintf("%s/%s.json", rd.saveDir, fbasepath)
	f, err := os.Create(jsonfile)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(pm.body); err != nil {
		return err
	}

	shscript := fmt.Sprintf("%s/%s.sh", rd.saveDir, fbasepath)
	logger.InfoContext(context.Background(), fmt.Sprintf("%s%s and %s has been saved", emoji("⌁", "yellow+b", rd.decorate), shscript, jsonfile))
	s, err := os.Create(shscript)
	if err != nil {
		return err
	}
	defer s.Close()

	var tmpl *template.Template
	var headers string
	if rd.useHttpie {
		tmpl = template.Must(template.New("shellScriptTmplHttpie").Parse(string(shellScriptHttpieTmpl)))
		headers = buildHttpieHeaders(pm.headers)
	} else {
		tmpl = template.Must(template.New("shellScriptTmpl").Parse(string(shellScriptTmpl)))
		headers = buildCurlHeaders(pm.headers)
	}

	if err := tmpl.Execute(s, struct {
		Headers       string
		TargetURL     string
		ContentType   string
		FileBase      string
		LocalDebugURL string
	}{
		Headers:       headers,
		TargetURL:     rd.targetURL,
		LocalDebugURL: rd.localDebugURL,
		ContentType:   shellQuote(pm.contentType),
		FileBase:      fbasepath,
	}); err != nil {
		return err
	}
	return os.Chmod(shscript, 0o755)
}

func buildHttpieHeaders(headers map[string]string) string {
	var b strings.Builder
	for k, v := range headers {
		fmt.Fprintf(&b, "%s ", shellQuote(fmt.Sprintf("%s:%s", k, v)))
	}
	return b.String()
}

type replayDataOpts struct {
	insecureTLSVerify           bool
	targetCnxTimeout            int
	sseBufferSize               int
	decorate, noReplay          bool
	saveDir, smeeURL, targetURL string
	localDebugURL               string
	ignoreEvents                []string
	useHttpie                   bool
	execCommand                 string
	execOnEvents                []string
	execEnvVars                 []string
	encryptionKeyFile           string
	encryptionKey               string
	resume                      bool
	clientID                    string
}

func replayData(ropts *replayDataOpts, logger *slog.Logger, pm payloadMsg) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ropts.targetCnxTimeout)*time.Second)
	defer cancel()
	//nolint:gosec // InsecureSkipVerify is controlled by user input for testing/self-signed certs
	client := http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: ropts.insecureTLSVerify}}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ropts.targetURL, strings.NewReader(string(pm.body)))
	if err != nil {
		return err
	}
	for k, v := range pm.headers {
		req.Header.Add(k, v)
	}
	if _, ok := pm.headers["Content-Type"]; !ok {
		req.Header.Add("Content-Type", pm.contentType)
	}
	resp, err := client.Do(req) //nolint:gosec // user-configured URL
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	msg := "request"
	if pm.eventType != "" {
		msg = fmt.Sprintf("%s event", pm.eventType)
	}
	if pm.eventID != "" {
		msg = fmt.Sprintf("%s %s", pm.eventID, msg)
	}
	msg = fmt.Sprintf("%s %s replayed to %s, status: %s", pm.timestamp, msg, ansi.Color(ropts.targetURL, "green+ub"), ansi.Color(fmt.Sprintf("%d", resp.StatusCode), "blue+b"))
	if resp.StatusCode > 299 {
		msg = fmt.Sprintf("%s, error: %s", msg, resp.Status)
	}
	s := fmt.Sprintf("%s%s", emoji("•", "magenta+b", ropts.decorate), msg)
	logger.InfoContext(context.Background(), s)
	return nil
}

func runExecCommand(ctx context.Context, rd *replayDataOpts, logger *slog.Logger, pm payloadMsg) error {
	if len(rd.execOnEvents) > 0 {
		if pm.eventType == "" {
			logger.DebugContext(ctx, "skipping exec: event has no type and exec-on-events filter is set")
			return nil
		}
		if !slices.Contains(rd.execOnEvents, pm.eventType) {
			logger.DebugContext(ctx,
				fmt.Sprintf("skipping exec for event type %s (not in exec-on-events list)", pm.eventType))
			return nil
		}
	}

	payloadFile, err := os.CreateTemp("", "gohookbridge-payload-*.json")
	if err != nil {
		return fmt.Errorf("failed to create payload temp file: %w", err)
	}
	defer os.Remove(payloadFile.Name())
	if _, err := payloadFile.Write(pm.body); err != nil {
		payloadFile.Close()
		return fmt.Errorf("failed to write payload temp file: %w", err)
	}
	payloadFile.Close()

	headersJSON, err := json.Marshal(pm.headers)
	if err != nil {
		return fmt.Errorf("failed to marshal headers: %w", err)
	}
	headersFile, err := os.CreateTemp("", "gohookbridge-headers-*.json")
	if err != nil {
		return fmt.Errorf("failed to create headers temp file: %w", err)
	}
	defer os.Remove(headersFile.Name())
	if _, err := headersFile.Write(headersJSON); err != nil {
		headersFile.Close()
		return fmt.Errorf("failed to write headers temp file: %w", err)
	}
	headersFile.Close()

	//nolint:gosec // Command is intentionally user-provided
	cmd := exec.CommandContext(ctx, "sh", "-c", rd.execCommand)
	cmd.Env = append(buildExecEnv(rd.execEnvVars),
		"GOSMEE_EVENT_TYPE="+pm.eventType,
		"GOSMEE_EVENT_ID="+pm.eventID,
		"GOSMEE_CONTENT_TYPE="+pm.contentType,
		"GOSMEE_TIMESTAMP="+pm.timestamp,
		"GOSMEE_PAYLOAD_FILE="+payloadFile.Name(),
		"GOSMEE_HEADERS_FILE="+headersFile.Name(),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err = cmd.Run(); err != nil {
		if stdout.Len() > 0 {
			logger.InfoContext(ctx,
				fmt.Sprintf("%sexec stdout: %s", emoji("→", "cyan+b", rd.decorate), strings.TrimSpace(stdout.String())))
		}
		if stderr.Len() > 0 {
			logger.InfoContext(ctx,
				fmt.Sprintf("%sexec stderr: %s", emoji("→", "yellow+b", rd.decorate), strings.TrimSpace(stderr.String())))
		}
		return fmt.Errorf("exec command failed: %w", err)
	}

	if stdout.Len() > 0 {
		logger.InfoContext(ctx,
			fmt.Sprintf("%sexec stdout: %s", emoji("→", "cyan+b", rd.decorate), strings.TrimSpace(stdout.String())))
	}
	if stderr.Len() > 0 {
		logger.InfoContext(ctx,
			fmt.Sprintf("%sexec stderr: %s", emoji("→", "yellow+b", rd.decorate), strings.TrimSpace(stderr.String())))
	}

	logger.InfoContext(ctx,
		fmt.Sprintf("%sexec command completed successfully for event %s", emoji("✓", "green+b", rd.decorate), pm.eventType))
	return nil
}

func buildExecEnv(extraVarNames []string) []string {
	allowlist := []string{
		"PATH",
		"HOME",
		"TMPDIR",
		"TEMP",
		"TMP",
		"USER",
		"USERNAME",
		"LOGNAME",
		"SHELL",
		"LANG",
		"LC_ALL",
		"LC_CTYPE",
		"TZ",
	}

	execEnv := make([]string, 0, len(allowlist)+len(extraVarNames))
	seen := map[string]struct{}{}
	addEnvVar := func(name string) {
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			return
		}
		execEnv = append(execEnv, name+"="+value)
		seen[name] = struct{}{}
	}

	for _, name := range allowlist {
		addEnvVar(name)
	}
	for _, envVar := range os.Environ() {
		name, _, ok := strings.Cut(envVar, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(name, "LC_") {
			addEnvVar(name)
		}
	}
	for _, name := range extraVarNames {
		addEnvVar(name)
	}

	return execEnv
}

func checkServerVersion(serverURL string, clientVersion string, logger *slog.Logger, decorate bool) error {
	baseURL := serverURL
	if parts := strings.Split(serverURL, "/"); len(parts) > 3 {
		baseURL = strings.Join(parts[0:3], "/")
	}

	versionURL := fmt.Sprintf("%s/version", baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(defaultTimeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		logger.WarnContext(context.Background(), fmt.Sprintf("%sCould not create version check request: %s", emoji("⚠", "yellow+b", decorate), err.Error()))
		return nil
	}

	client := http.Client{Timeout: time.Duration(defaultTimeout) * time.Second}
	resp, err := client.Do(req) //nolint:gosec // user-configured URL
	if err != nil {
		logger.WarnContext(context.Background(), fmt.Sprintf("%sCould not check server version: %s", emoji("⚠", "yellow+b", decorate), err.Error()))
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		errMsg := fmt.Sprintf("%sThe server appears to be too old and doesn't support version checking. Please upgrade the server or use an older client version.",
			emoji("⛔", "red+b", decorate))
		logger.ErrorContext(context.Background(), errMsg)
		return fmt.Errorf("%s", errMsg)
	}

	if resp.StatusCode != http.StatusOK {
		logger.WarnContext(context.Background(), fmt.Sprintf("%sServer returned unexpected status code %d when checking version",
			emoji("⚠", "yellow+b", decorate), resp.StatusCode))
		return nil
	}

	serverVersion := resp.Header.Get("X-Gosmee-Version")

	if serverVersion == "" {
		var versionResp struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&versionResp); err != nil {
			logger.WarnContext(context.Background(), fmt.Sprintf("%sCould not parse server version: %s", emoji("⚠", "yellow+b", decorate), err.Error()))
			return nil
		}
		serverVersion = versionResp.Version
	}

	if serverVersion != "" {
		if serverVersion == clientVersion {
			logger.DebugContext(context.Background(), fmt.Sprintf("Version match: client and server both at version %s", serverVersion))
		} else {
			if serverVersion == "dev" || clientVersion == "dev" {
				logger.WarnContext(context.Background(), fmt.Sprintf("%sVersion mismatch with development version: client %s, server %s",
					emoji("⚠", "yellow+b", decorate),
					ansi.Color(clientVersion, "blue+b"),
					ansi.Color(serverVersion, "blue+b")))
			} else {
				serverParts := parseVersion(serverVersion)
				clientParts := parseVersion(clientVersion)

				isClientOutdated := isOlderVersion(clientParts, serverParts)

				if isClientOutdated {
					errMsg := fmt.Sprintf("%sClient version %s is too old. Server version is %s. Please upgrade your gohookbridge client.",
						emoji("⛔", "red+b", decorate),
						ansi.Color(clientVersion, "blue+b"),
						ansi.Color(serverVersion, "blue+b"))
					logger.ErrorContext(context.Background(), errMsg)
					return fmt.Errorf("%s", errMsg)
				}
				logger.WarnContext(context.Background(), fmt.Sprintf("%sVersion mismatch: client %s, server %s",
					emoji("⚠", "yellow+b", decorate),
					ansi.Color(clientVersion, "blue+b"),
					ansi.Color(serverVersion, "blue+b")))
			}
		}
	}

	logger.InfoContext(context.Background(), fmt.Sprintf("%sServer version: %s", emoji("✓", "green+b", decorate), serverVersion))
	return nil
}

func parseVersion(version string) []int {
	version = strings.TrimPrefix(version, "v")
	parts := strings.Split(version, ".")
	result := make([]int, 0, len(parts))

	for _, part := range parts {
		for i, c := range part {
			if c < '0' || c > '9' {
				part = part[:i]
				break
			}
		}

		num, err := strconv.Atoi(part)
		if err != nil {
			num = 0
		}
		result = append(result, num)
	}

	for len(result) < 3 {
		result = append(result, 0)
	}

	return result
}

func isOlderVersion(v1, v2 []int) bool {
	minLen := min(len(v1), len(v2))

	for i := 0; i < minLen; i++ {
		if v1[i] < v2[i] {
			return true
		} else if v1[i] > v2[i] {
			return false
		}
	}

	return len(v1) < len(v2)
}

func prepareSubscription(smeeURL, encryptionKeyFile string, resume bool, clientID string) (channel string, sseURL string, privateKey *[32]byte, err error) {
	channel = filepath.Base(smeeURL)
	baseURL := strings.TrimSuffix(smeeURL, "/"+channel)

	if strings.HasPrefix(smeeURL, "https://smee.io") {
		if encryptionKeyFile != "" {
			return "", "", nil, fmt.Errorf("client key files are only supported with gohookbridge server URLs, not https://smee.io")
		}
		return smeeChannel, smeeURL, nil, nil
	}

	sseURL = fmt.Sprintf("%s/events/%s", baseURL, channel)

	if resume {
		if clientID == "" {
			clientID = getOrCreateClientID()
		}
		parsedURL, err := url.Parse(sseURL)
		if err != nil {
			return "", "", nil, fmt.Errorf("parse sse url: %w", err)
		}
		query := parsedURL.Query()
		query.Set("client_id", clientID)
		parsedURL.RawQuery = query.Encode()
		sseURL = parsedURL.String()
	}

	if encryptionKeyFile == "" {
		return channel, sseURL, nil, nil
	}

	publicKey, loadedPrivateKey, err := gohookbridge.LoadKeyPair(encryptionKeyFile)
	if err != nil {
		return "", "", nil, fmt.Errorf("load encryption keys: %w", err)
	}

	parsedURL, err := url.Parse(sseURL)
	if err != nil {
		return "", "", nil, fmt.Errorf("parse sse url: %w", err)
	}
	query := parsedURL.Query()
	query.Set("pubkey", gohookbridge.EncodePublicKey(publicKey))
	parsedURL.RawQuery = query.Encode()

	return channel, parsedURL.String(), loadedPrivateKey, nil
}

func (c goSmee) clientSetup() error {
	version := strings.TrimSpace(Version)
	s := fmt.Sprintf("%sStarting gohookbridge client version: %s", emoji("⇉", "green+b", c.replayDataOpts.decorate), version)
	c.logger.InfoContext(context.Background(), s)

	if err := checkServerVersion(c.replayDataOpts.smeeURL, version, c.logger, c.replayDataOpts.decorate); err != nil {
		c.logger.WarnContext(context.Background(), fmt.Sprintf("%sCould not get server version: %s", emoji("⚠", "yellow+b", c.replayDataOpts.decorate), err.Error()))
	}

	channel, sseURL, privateKey, err := prepareSubscription(c.replayDataOpts.smeeURL, c.replayDataOpts.encryptionKeyFile, c.replayDataOpts.resume, c.replayDataOpts.clientID)
	if err != nil {
		return err
	}
	if privateKey != nil {
		c.logger.InfoContext(context.Background(), fmt.Sprintf("%sProtected channel mode enabled for gohookbridge SSE transport", emoji("🔐", "green+b", c.replayDataOpts.decorate)))
	}

	client := sse.NewClient(sseURL, sse.ClientMaxBufferSize(c.replayDataOpts.sseBufferSize))
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.MaxElapsedTime = 0
	client.ReconnectStrategy = expBackoff
	c.logger.InfoContext(context.Background(), fmt.Sprintf("%sConfigured reconnection strategy to retry indefinitely", emoji("⇉", "blue+b", c.replayDataOpts.decorate)))
	client.Headers["User-Agent"] = fmt.Sprintf("gohookbridge/%s", version)
	client.Headers["X-Accel-Buffering"] = "no"

	return client.Subscribe(channel, func(msg *sse.Event) {
		now := time.Now().UTC()
		nowStr := now.Format(tsFormat)

		if string(msg.Event) == "ready" || string(msg.Data) == "ready" {
			var s string
			if c.replayDataOpts.targetURL != "" {
				s = fmt.Sprintf("%s %sForwarding %s to %s", nowStr, emoji("✓", "yellow+b", c.replayDataOpts.decorate), ansi.Color(c.replayDataOpts.smeeURL, "green+u"), ansi.Color(c.replayDataOpts.targetURL, "green+u"))
			} else {
				s = fmt.Sprintf("%s %sListening on %s", nowStr, emoji("✓", "yellow+b", c.replayDataOpts.decorate), ansi.Color(c.replayDataOpts.smeeURL, "green+u"))
			}
			c.logger.InfoContext(context.Background(), s)
			return
		}

		if string(msg.Event) == "ping" {
			return
		}

		if len(msg.Data) == 0 || string(msg.Data) == "{}" {
			return
		}

		if strings.Contains(strings.ToLower(string(msg.Data)), "ready") ||
			strings.Contains(strings.ToLower(string(msg.Data)), "\"message\"") &&
				strings.Contains(strings.ToLower(string(msg.Data)), "\"connected\"") {
			c.logger.DebugContext(context.Background(), fmt.Sprintf("%s Skipping connection message", nowStr))
			return
		}

		payload := msg.Data
		if c.replayDataOpts.encryptionKey != "" && gohookbridge.IsAESEncrypted(msg.Data) {
			decryptedPayload, err := gohookbridge.AESDecrypt(msg.Data, c.replayDataOpts.encryptionKey)
			if err != nil {
				s := fmt.Sprintf("%s %s AES decrypting message %s", nowStr, ansi.Color("ERROR", "red+b"), err.Error())
				c.logger.ErrorContext(context.Background(), s)
				return
			}
			payload = decryptedPayload
		}

		pm, err := c.parse(now, payload)
		if err != nil {
			s := fmt.Sprintf("%s %s parsing message %s", nowStr, ansi.Color("ERROR", "red+b"), err.Error())
			c.logger.ErrorContext(context.Background(), s)
			return
		}

		if privateKey != nil && gohookbridge.IsEncrypted(pm.body) {
			decryptedBody, err := gohookbridge.Decrypt(pm.body, privateKey)
			if err != nil {
				s := fmt.Sprintf("%s %s decrypting message body %s", nowStr, ansi.Color("ERROR", "red+b"), err.Error())
				c.logger.ErrorContext(context.Background(), s)
				return
			}
			pm.body = decryptedBody
		} else if c.replayDataOpts.encryptionKey != "" && gohookbridge.IsAESEncrypted(pm.body) {
			decryptedBody, err := gohookbridge.AESDecrypt(pm.body, c.replayDataOpts.encryptionKey)
			if err != nil {
				s := fmt.Sprintf("%s %s AES decrypting message body %s", nowStr, ansi.Color("ERROR", "red+b"), err.Error())
				c.logger.ErrorContext(context.Background(), s)
				return
			}
			pm.body = decryptedBody
		}

		if pm.eventType == "ready" || ((len(pm.body) > 0) && strings.ToLower(string(pm.body)) == "ready") {
			c.logger.DebugContext(context.Background(), fmt.Sprintf("%s Skipping message with 'ready' in event type or body", nowStr))
			return
		}

		if len(pm.body) == 0 {
			for k, v := range pm.headers {
				if strings.EqualFold(k, "Message") && strings.EqualFold(v, "connected") {
					c.logger.DebugContext(context.Background(), fmt.Sprintf("%s Skipping empty message with Message: connected header", nowStr))
					return
				}
			}
		}

		if len(pm.headers) == 0 {
			s := fmt.Sprintf("%s %s no headers found in message", nowStr, ansi.Color("ERROR", "red+b"))
			c.logger.ErrorContext(context.Background(), s)
			return
		}

		headers := buildHeaders(pm.headers)
		if c.replayDataOpts.saveDir != "" {
			if err := saveData(c.replayDataOpts, c.logger, pm); err != nil {
				s := fmt.Sprintf("%s %s saving message with headers '%s' - %s", nowStr, ansi.Color("ERROR", "red+b"), headers, err.Error())
				c.logger.ErrorContext(context.Background(), s)
				return
			}
		}

		if !c.replayDataOpts.noReplay {
			if err := replayData(c.replayDataOpts, c.logger, pm); err != nil {
				s := fmt.Sprintf("%s %s forwarding message with headers '%s' - %s", nowStr, ansi.Color("ERROR", "red+b"), headers, err.Error())
				c.logger.ErrorContext(context.Background(), s)
				return
			}
		}

		if c.replayDataOpts.execCommand != "" {
			if err := runExecCommand(context.Background(), c.replayDataOpts, c.logger, pm); err != nil {
				s := fmt.Sprintf("%s %s exec command failed for event '%s' - %s", nowStr, ansi.Color("ERROR", "red+b"), pm.eventType, err.Error())
				c.logger.ErrorContext(context.Background(), s)
			}
		}
	})
}

func serveHealthEndpoint(port int, logger *slog.Logger, decorate bool) {
	if port <= 0 {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", retVersion)

	addr := fmt.Sprintf(":%d", port)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.InfoContext(context.Background(), fmt.Sprintf("%sStarting health server on %s", emoji("✓", "green+b", decorate), addr))

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.ErrorContext(context.Background(), fmt.Sprintf("%sHealth server error: %s", emoji("⛔", "red+b", decorate), err.Error()))
		}
	}()
}

func retVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Gosmee-Version", Version)
	resp := map[string]string{
		"version": Version,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
