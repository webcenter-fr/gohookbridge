{{/*
Raft peers string: "server-0=server-0.<headless>.<ns>.svc.<domain>:6001,..."
(DNS names, cluster-domain aware). Static fallback; the dnsPeerResolver is
authoritative when the StatefulSet/headless flags are set.
*/}}
{{- define "gohookbridge.raftPeers" -}}
{{- $name := include "gohookbridge.fullname" . -}}
{{- $ns := .Release.Namespace -}}
{{- $replicas := int .Values.server.replicas -}}
{{- $domain := .Values.server.raft.clusterDomain | default "cluster.local" -}}
{{- range $i := until $replicas -}}
{{- if $i }},{{ end -}}
{{- $host := printf "%s-server-%d.%s-server-headless.%s.svc" $name $i $name $ns -}}
{{- if $domain }}{{- $host = printf "%s.%s" $host $domain }}{{ end -}}
{{ $name }}-server-{{ $i }}={{ $host }}:{{ $.Values.server.raftPort }}
{{- end -}}
{{- end }}

{{/*
NATS routes string: "nats://server-0.<headless>.<ns>.svc.<domain>:6222,..."
(cluster-domain aware).
*/}}
{{- define "gohookbridge.natsRoutes" -}}
{{- $name := include "gohookbridge.fullname" . -}}
{{- $ns := .Release.Namespace -}}
{{- $replicas := int .Values.server.replicas -}}
{{- $domain := .Values.server.raft.clusterDomain | default "cluster.local" -}}
{{- range $i := until $replicas -}}
{{- if $i }},{{ end -}}
{{- $host := printf "%s-server-%d.%s-server-headless.%s.svc" $name $i $name $ns -}}
{{- if $domain }}{{- $host = printf "%s.%s" $host $domain }}{{ end -}}
nats://{{ $host }}:{{ $.Values.server.natsClusterPort }}
{{- end -}}
{{- end }}

{{/*
Raft advertise FQDN for the pod hostname (POD_NAME is expanded by the shell).
*/}}
{{- define "gohookbridge.raftAdvertise" -}}
{{- $name := include "gohookbridge.fullname" . -}}
{{- $ns := .Release.Namespace -}}
{{- $domain := .Values.server.raft.clusterDomain | default "cluster.local" -}}
{{- $host := printf "%s-server-headless.%s.svc" $name $ns -}}
{{- if $domain }}{{- $host = printf "%s.%s" $host $domain }}{{ end -}}
{{- printf "$(POD_NAME).%s:%v" $host .Values.server.raftPort -}}
{{- end }}

{{/*
Raft CA Secret name.
*/}}
{{- define "gohookbridge.raftCASecret" -}}
{{- .Values.server.raft.tls.caSecret | default (printf "%s-raft-ca" (include "gohookbridge.fullname" .)) -}}
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

{{/*
Resolve server.publicPort to a canonical integer, validating it. Accepts an
unquoted integer (8082) or a quoted numeric string ("8082"). Fails the render
on any non-numeric value so a misconfigured port cannot silently disable the
public listener (sprig's int/cast.ToInt would coerce garbage to 0). 0 (or
unset) is the disabled sentinel and renders byte-identically to the
pre-change chart.
*/}}
{{- define "gohookbridge.publicPort" -}}
{{- $raw := .Values.server.publicPort -}}
{{- if eq (kindOf $raw) "string" -}}
  {{- $t := trim $raw | trimAll "\"" -}}
  {{- if regexMatch `^[0-9]+$` $t -}}
    {{- atoi $t -}}
  {{- else -}}
    {{- fail (printf "server.publicPort must be a non-negative integer (0 disables the public listener), got %q" $raw) -}}
  {{- end -}}
{{- else if eq (kindOf $raw) "int" -}}
  {{- $raw -}}
{{- else if eq (kindOf $raw) "int64" -}}
  {{- $raw -}}
{{- else if eq (kindOf $raw) "float64" -}}
  {{- printf "%.0f" $raw -}}
{{- else if eq (kindOf $raw) "invalid" -}}
  {{- "0" -}}
{{- else -}}
  {{- fail (printf "server.publicPort has unsupported type %s" (kindOf $raw)) -}}
{{- end -}}
{{- end -}}
