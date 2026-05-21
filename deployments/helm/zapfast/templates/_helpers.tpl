{{/*
Expand the name of the chart.
*/}}
{{- define "zapfast.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "zapfast.fullname" -}}
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
Chart name + version label.
*/}}
{{- define "zapfast.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "zapfast.labels" -}}
helm.sh/chart: {{ include "zapfast.chart" . }}
{{ include "zapfast.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: zapfast
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "zapfast.selectorLabels" -}}
app.kubernetes.io/name: {{ include "zapfast.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name to use.
*/}}
{{- define "zapfast.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "zapfast.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Secret name: existing user-provided Secret or chart-managed one.
*/}}
{{- define "zapfast.secretName" -}}
{{- if .Values.db.existingSecret }}
{{- .Values.db.existingSecret }}
{{- else }}
{{- printf "%s-secret" (include "zapfast.fullname" .) }}
{{- end }}
{{- end }}

{{/*
ConfigMap name.
*/}}
{{- define "zapfast.configMapName" -}}
{{- printf "%s-config" (include "zapfast.fullname" .) }}
{{- end }}
