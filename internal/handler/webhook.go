package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"github.com/webcenter-fr/gohookbridge/pkg/crypto"
	"github.com/webcenter-fr/gohookbridge/pkg/encryption"
	"github.com/webcenter-fr/gohookbridge/pkg/nats"
	"github.com/webcenter-fr/gohookbridge/pkg/uuid"
)

const (
	timeFormat        = "2006-01-02T15.04.01.000"
	contentType       = "application/json"
	versionHeaderName = "X-Gohookbridge-Version"
	maxChannelLength  = 64
	ChannelIDPattern  = "[a-zA-Z0-9_-]{1,64}"
	ChannelPath       = "/{channel:" + ChannelIDPattern + "}"
	EventsPath        = "/events/{channel:" + ChannelIDPattern + "}"
	EventIDPattern    = "[a-zA-Z0-9-]{1,64}"
)

var eventIDPatternRe = regexp.MustCompile("^" + EventIDPattern + "$")

func EffectivePublicURL(publicURL, portAddr string, sslEnabled bool) string {
	if publicURL != "" {
		return publicURL
	}

	scheme := "http://"
	if sslEnabled {
		scheme = "https://"
	}

	return fmt.Sprintf("%s%s", scheme, portAddr)
}

func errorIt(w http.ResponseWriter, _ *http.Request, status int, err error) {
	w.WriteHeader(status)
	_, _ = w.Write([]byte(err.Error()))
}

func validateGitHubWebhookSignature(secret string, payload []byte, signatureHeader string) bool {
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}

	signature := strings.TrimPrefix(signatureHeader, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

func validateBitbucketHMAC(secret string, payload []byte, signatureHeader string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signatureHeader), []byte(expectedMAC))
}

func validateGiteaSignature(secret string, payload []byte, signatureHeader string) bool {
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}

	signature := strings.TrimPrefix(signatureHeader, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

func validateWebhookSignature(secret string, payload []byte, r *http.Request) bool {
	if secret == "" {
		return true
	}

	if gitlabToken := r.Header.Get("X-Gitlab-Token"); gitlabToken != "" {
		return subtle.ConstantTimeCompare([]byte(gitlabToken), []byte(secret)) == 1
	}

	if githubSignature := r.Header.Get("X-Hub-Signature-256"); githubSignature != "" {
		fmt.Fprintf(os.Stdout, "Received request %s %s\n", r.Method, r.URL.Path)
		return validateGitHubWebhookSignature(secret, payload, githubSignature)
	}

	if bitbucketSignature := r.Header.Get("X-Hub-Signature"); bitbucketSignature != "" {
		return validateBitbucketHMAC(secret, payload, bitbucketSignature)
	}

	if giteaSignature := r.Header.Get("X-Gitea-Signature"); giteaSignature != "" {
		return validateGiteaSignature(secret, payload, giteaSignature)
	}

	return false
}

func HandleWebhookPost(broker *nats.Broker, svc *service.Service, banTracker *service.BanTracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		now := time.Now().UTC()
		if !strings.Contains(r.Header.Get("Content-Type"), contentType) {
			http.Error(w, "content-type must be application/json", http.StatusBadRequest)
			return
		}
		channel := chi.URLParam(r, "channel")
		defer r.Body.Close()

		chConfig, _ := svc.ResolveChannelConfig(ctx, channel)

		maxBodySize := chConfig.MaxBodySize
		if maxBodySize == 0 {
			maxBodySize, _ = svc.ResolveChannelMaxBodySize(ctx, channel)
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodySize))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		encryptionMode := chConfig.EncryptionMode
		encryptionKey := chConfig.EncryptionKey
		channelPubKey := chConfig.EncryptionPublicKey
		if encryptionMode == "" {
			encryptionMode, encryptionKey, channelPubKey, _ = svc.ResolveChannelEncryption(ctx, channel)
		}

		var payloadBytes []byte

		switch {
		case encryptionMode == "e2e" && channelPubKey != "":
			webhookSecret := chConfig.WebhookSecret
			if webhookSecret == "" {
				webhookSecret, _ = svc.ResolveChannelWebhookSecret(ctx, channel)
			}

			if crypto.IsEncrypted(body) {
				if webhookSecret != "" {
					if !validateWebhookSignature(webhookSecret, body, r) {
						svc.RecordCredentialFailure(ctx, banTracker, r, service.FingerprintSignature(channel, service.ExtractSignatureValue(r)))
						http.Error(w, "invalid signature", http.StatusUnauthorized)
						return
					}
				}
				payloadBytes = body
			} else {
				if webhookSecret != "" {
					if !validateWebhookSignature(webhookSecret, body, r) {
						svc.RecordCredentialFailure(ctx, banTracker, r, service.FingerprintSignature(channel, service.ExtractSignatureValue(r)))
						http.Error(w, "invalid signature", http.StatusUnauthorized)
						return
					}
				}
				var d any
				if err := json.Unmarshal(body, &d); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				pubKey, err := crypto.ParsePublicKey(channelPubKey)
				if err != nil {
					fmt.Fprintf(os.Stderr, "WARNING: parse channel public key failed for channel %s: %v\n", channel, err)
					http.Error(w, "encryption config error", http.StatusInternalServerError)
					return
				}
				encrypted, err := crypto.Encrypt(body, pubKey)
				if err != nil {
					fmt.Fprintf(os.Stderr, "WARNING: e2e encryption failed for channel %s: %v\n", channel, err)
					http.Error(w, "encryption failed", http.StatusInternalServerError)
					return
				}
				payloadBytes = encrypted
			}
		case encryptionMode == "server_side" && encryptionKey != "":
			webhookSecret := chConfig.WebhookSecret
			if webhookSecret == "" {
				webhookSecret, _ = svc.ResolveChannelWebhookSecret(ctx, channel)
			}
			if webhookSecret != "" {
				if !validateWebhookSignature(webhookSecret, body, r) {
					svc.RecordCredentialFailure(ctx, banTracker, r, service.FingerprintSignature(channel, service.ExtractSignatureValue(r)))
					http.Error(w, "invalid signature", http.StatusUnauthorized)
					return
				}
			}

			var d any
			if err := json.Unmarshal(body, &d); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			encrypted, err := encryption.AESEncrypt(body, encryptionKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: server-side encryption failed for channel %s: %v\n", channel, err)
				http.Error(w, "encryption failed", http.StatusInternalServerError)
				return
			}
			payloadBytes = encrypted
		default:
			webhookSecret := chConfig.WebhookSecret
			if webhookSecret == "" {
				webhookSecret, _ = svc.ResolveChannelWebhookSecret(ctx, channel)
			}
			if webhookSecret != "" {
				if !validateWebhookSignature(webhookSecret, body, r) {
					svc.RecordCredentialFailure(ctx, banTracker, r, service.FingerprintSignature(channel, service.ExtractSignatureValue(r)))
					http.Error(w, "invalid signature", http.StatusUnauthorized)
					return
				}
			}

			var d any
			if err := json.Unmarshal(body, &d); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			payloadBytes = body
		}

		var headersBuilder strings.Builder
		payload := make(map[string]any)
		for k, v := range r.Header {
			// The log line must never leak credentials: webhook secrets
			// (X-Gitlab-Token), signatures, Authorization and cookies are
			// redacted from stdout logs (CWE-532). The downstream event payload
			// keeps the full headers for forwarding.
			if sensitiveHeaderForLogs(k) {
				fmt.Fprintf(&headersBuilder, " %s=<redacted>", k)
			} else {
				fmt.Fprintf(&headersBuilder, " %s=%s", k, v[0])
			}
			payload[strings.ToLower(k)] = v[0]
		}
		payload["timestamp"] = fmt.Sprintf("%d", now.UnixMilli())
		payload["bodyB"] = base64.StdEncoding.EncodeToString(payloadBytes)
		eventID, err := uuid.GenerateUUID()
		if err != nil {
			http.Error(w, "Failed to generate event id", http.StatusInternalServerError)
			return
		}
		payload["event_id"] = eventID
		reencoded, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if chConfig.MessageTTLSeconds > 0 {
			broker.SetChannelTTL(channel, time.Duration(chConfig.MessageTTLSeconds)*time.Second)
		}

		publishEvent(broker, channel, reencoded)

		w.Header().Set(versionHeaderName, strings.TrimSpace(string(Version)))
		w.WriteHeader(http.StatusAccepted)
		resp := map[string]any{
			"status":   http.StatusAccepted,
			"channel":  channel,
			"message":  "ok",
			"version":  strings.TrimSpace(string(Version)),
			"event_id": eventID,
		}
		_ = json.NewEncoder(w).Encode(resp)
		fmt.Fprintf(os.Stdout, "%s Published %s%s on channel %s\n",
			now.Format(timeFormat),
			middleware.GetReqID(r.Context()),
			headersBuilder.String(),
			channel)
	}
}

func HandleTestPayloadSend(broker *nats.Broker, svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		if channel == "" {
			http.Error(w, "Channel name missing in URL", http.StatusBadRequest)
			return
		}
		if len(channel) > maxChannelLength {
			http.Error(w, "Channel name exceeds maximum length", http.StatusBadRequest)
			return
		}

		now := time.Now().UTC()
		defer r.Body.Close()

		maxBodySize, _ := svc.ResolveChannelMaxBodySize(r.Context(), channel)
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodySize))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if !json.Valid(body) {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		payload := make(map[string]any)
		payload["timestamp"] = fmt.Sprintf("%d", now.UnixMilli())
		payload["bodyB"] = base64.StdEncoding.EncodeToString(body)
		payload["content-type"] = contentType
		payload["x-test-payload"] = "true"

		reencoded, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		publishEvent(broker, channel, reencoded)

		w.WriteHeader(http.StatusAccepted)
		resp := map[string]any{
			"status":  http.StatusAccepted,
			"channel": channel,
			"message": "test payload sent",
		}
		_ = json.NewEncoder(w).Encode(resp)
		fmt.Fprintf(os.Stdout, "%s Test payload sent on channel %s\n",
			now.Format(timeFormat), channel)
	}
}

func HandleEventReplay(broker *nats.Broker, svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		channel := chi.URLParam(r, "channel")
		eventID := chi.URLParam(r, "eventId")
		if channel == "" || eventID == "" {
			http.Error(w, "channel and eventId required", http.StatusBadRequest)
			return
		}
		if !eventIDPatternRe.MatchString(eventID) {
			http.Error(w, "invalid eventId", http.StatusBadRequest)
			return
		}

		ch, err := svc.ResolveChannelConfig(ctx, channel)
		if err != nil {
			http.Error(w, "channel not found", http.StatusNotFound)
			return
		}
		if ch.EncryptionMode == "server_side" && ch.EncryptionKey != "" {
			http.Error(w, "cannot replay events for server-side encrypted channels via API", http.StatusBadRequest)
			return
		}

		replayBody, err := json.Marshal(map[string]any{"replayed": true, "original_event_id": eventID})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var bodyB string
		if ch.EncryptionMode == "e2e" && ch.EncryptionPublicKey != "" {
			pubKey, err := crypto.ParsePublicKey(ch.EncryptionPublicKey)
			if err != nil {
				http.Error(w, "encryption config error", http.StatusInternalServerError)
				return
			}
			encrypted, err := crypto.Encrypt(replayBody, pubKey)
			if err != nil {
				http.Error(w, "encryption failed", http.StatusInternalServerError)
				return
			}
			bodyB = base64.StdEncoding.EncodeToString(encrypted)
		} else {
			bodyB = base64.StdEncoding.EncodeToString(replayBody)
		}

		payload := map[string]any{
			"timestamp": fmt.Sprintf("%d", time.Now().UTC().UnixMilli()),
			"event_id":  eventID,
			"bodyB":     bodyB,
			"x-replay":  "true",
		}
		reencoded, _ := json.Marshal(payload)
		publishEvent(broker, channel, reencoded)

		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "replayed", "event_id": eventID})
	}
}

func HandleGenerateEncryptionKey(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		channel := chi.URLParam(r, "channel")
		ch, err := svc.GetChannel(ctx, channel)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				http.Error(w, "channel not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var req struct {
			Mode string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Mode == "" {
			req.Mode = "server_side"
		}

		switch req.Mode {
		case "server_side":
			key, err := encryption.GenerateAESKey()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			ch.EncryptionMode = "server_side"
			ch.EncryptionKey = key
			if err := svc.UpdateChannel(ctx, ch); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSONResponse(w, http.StatusOK, map[string]string{
				"encryption_key":  key,
				"encryption_mode": "server_side",
			})
		default:
			http.Error(w, "unsupported encryption mode: "+req.Mode, http.StatusBadRequest)
		}
	}
}

func writeJSONResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func publishEvent(broker *nats.Broker, channel string, reencoded []byte) {
	if err := broker.Publish(channel, reencoded); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: nats publish error: %v\n", err)
	}
}

// sensitiveHeaderForLogs reports whether a request header carries credentials
// that must be redacted from stdout logs.
func sensitiveHeaderForLogs(header string) bool {
	switch strings.ToLower(header) {
	case "authorization", "proxy-authorization", "cookie",
		"x-gitlab-token", "x-hub-signature", "x-hub-signature-256",
		"x-gitea-signature", "x-amz-signature":
		return true
	default:
		return false
	}
}

// Version is the build version injected by the version file in
// internal/app/templates; the handler uses it in API responses.
var Version []byte
