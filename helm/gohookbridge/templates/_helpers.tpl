{{/*
Raft peers string: "server-0=server-0.<headless>.<ns>.svc:6001,server-1=..."
*/}}
{{- define "gohookbridge.raftPeers" -}}
{{- $name := include "gohookbridge.fullname" . -}}
{{- $ns := .Release.Namespace -}}
{{- $replicas := int .Values.server.replicas -}}
{{- range $i := until $replicas -}}
{{- if $i }},{{ end -}}
{{ $name }}-server-{{ $i }}={{ $name }}-server-{{ $i }}.{{ $name }}-server-headless.{{ $ns }}.svc:{{ $.Values.server.raftPort }}
{{- end -}}
{{- end }}

{{/*
NATS routes string: "nats://server-0.<headless>.<ns>.svc:6222,nats://..."
*/}}
{{- define "gohookbridge.natsRoutes" -}}
{{- $name := include "gohookbridge.fullname" . -}}
{{- $ns := .Release.Namespace -}}
{{- $replicas := int .Values.server.replicas -}}
{{- range $i := until $replicas -}}
{{- if $i }},{{ end -}}
nats://{{ $name }}-server-{{ $i }}.{{ $name }}-server-headless.{{ $ns }}.svc:{{ $.Values.server.natsClusterPort }}
{{- end -}}
{{- end }}

{{/*
Expand the name of the chart.
*/}}
{{- define "gohookbridge.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "gohookbridge.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "gohookbridge.labels" -}}
helm.sh/chart: {{ include "gohookbridge.name" . }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "gohookbridge.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Encryption secret name
*/}}
{{- define "gohookbridge.encryptionSecretName" -}}
{{- if .Values.encryption.existingSecret }}
{{- .Values.encryption.existingSecret }}
{{- else }}
{{- include "gohookbridge.fullname" . }}-encryption-key
{{- end }}
{{- end }}
