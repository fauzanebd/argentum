{{/*
Expand the name of the chart.
*/}}
{{- define "argentum.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name (release-wide).
*/}}
{{- define "argentum.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Per-component fullnames.
*/}}
{{- define "argentum.api.fullname" -}}
{{- printf "%s-api" (include "argentum.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "argentum.worker.fullname" -}}
{{- printf "%s-worker" (include "argentum.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Chart label.
*/}}
{{- define "argentum.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels (release-wide).
*/}}
{{- define "argentum.labels" -}}
helm.sh/chart: {{ include "argentum.chart" . }}
{{ include "argentum.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels (release-wide).
*/}}
{{- define "argentum.selectorLabels" -}}
app.kubernetes.io/name: {{ include "argentum.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Per-component labels.
*/}}
{{- define "argentum.api.labels" -}}
{{ include "argentum.labels" . }}
app.kubernetes.io/component: api
{{- end }}

{{- define "argentum.api.selectorLabels" -}}
{{ include "argentum.selectorLabels" . }}
app.kubernetes.io/component: api
{{- end }}

{{- define "argentum.worker.labels" -}}
{{ include "argentum.labels" . }}
app.kubernetes.io/component: worker
{{- end }}

{{- define "argentum.worker.selectorLabels" -}}
{{ include "argentum.selectorLabels" . }}
app.kubernetes.io/component: worker
{{- end }}

{{- define "argentum.discord.fullname" -}}
{{- printf "%s-discord" (include "argentum.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "argentum.discord.labels" -}}
{{ include "argentum.labels" . }}
app.kubernetes.io/component: discord
{{- end }}

{{- define "argentum.discord.selectorLabels" -}}
{{ include "argentum.selectorLabels" . }}
app.kubernetes.io/component: discord
{{- end }}

{{/*
Service account name.
*/}}
{{- define "argentum.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "argentum.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Shared secret name (Bitwarden-managed or pre-existing).
*/}}
{{- define "argentum.secretName" -}}
{{- if .Values.existingSecret -}}
{{- .Values.existingSecret -}}
{{- else -}}
{{- printf "%s-secret" (include "argentum.fullname" .) -}}
{{- end -}}
{{- end }}

{{/*
Image reference helper. Usage: {{ include "argentum.image" (dict "ctx" . "repository" .Values.api.image.repository) }}
*/}}
{{- define "argentum.image" -}}
{{- $tag := default .ctx.Chart.AppVersion .ctx.Values.image.tag -}}
{{- if .ctx.Values.image.registry -}}
{{- printf "%s/%s:%s" .ctx.Values.image.registry .repository $tag -}}
{{- else -}}
{{- printf "%s:%s" .repository $tag -}}
{{- end -}}
{{- end }}

{{/*
Init containers shared by api + worker (wait-for-postgres, wait-for-redis).
*/}}
{{- define "argentum.initContainers" -}}
{{- if .Values.initContainers.waitForPostgres.enabled }}
- name: wait-for-postgres
  image: {{ .Values.initContainers.waitForPostgres.image }}
  command:
    - sh
    - -c
    - |
      until nc -z {{ .Values.initContainers.waitForPostgres.dbHost }} {{ .Values.initContainers.waitForPostgres.dbPort }}; do
        echo "Waiting for Postgres at {{ .Values.initContainers.waitForPostgres.dbHost }}:{{ .Values.initContainers.waitForPostgres.dbPort }}..."
        sleep 2
      done
      echo "Postgres is available"
  securityContext:
    allowPrivilegeEscalation: false
    capabilities:
      drop:
        - ALL
    readOnlyRootFilesystem: true
    runAsNonRoot: true
    runAsUser: 1000
{{- end }}
{{- if .Values.initContainers.waitForRedis.enabled }}
- name: wait-for-redis
  image: {{ .Values.initContainers.waitForRedis.image }}
  command:
    - sh
    - -c
    - |
      until nc -z {{ .Values.initContainers.waitForRedis.redisHost }} {{ .Values.initContainers.waitForRedis.redisPort }}; do
        echo "Waiting for Redis at {{ .Values.initContainers.waitForRedis.redisHost }}:{{ .Values.initContainers.waitForRedis.redisPort }}..."
        sleep 2
      done
      echo "Redis is available"
  securityContext:
    allowPrivilegeEscalation: false
    capabilities:
      drop:
        - ALL
    readOnlyRootFilesystem: true
    runAsNonRoot: true
    runAsUser: 1000
{{- end }}
{{- end }}
