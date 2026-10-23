{{- define "app.name" -}}{{ .Release.Name }}{{- end -}}

{{- define "app.labels" -}}
app.kubernetes.io/name: {{ include "app.name" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "app.selector" -}}
app.kubernetes.io/name: {{ include "app.name" . }}
{{- end -}}

{{- define "app.hcPath" -}}
{{- default (default "/" .Values.probe.httpPath) .Values.ingress.healthcheckPath -}}
{{- end -}}

{{/* Validate that scaling.mode is one of the supported values */}}
{{- define "app.mode" -}}
{{- $m := .Values.scaling.mode -}}
{{- if not (has $m (list "none" "hpa" "http" "sqs")) -}}
{{- fail (printf "scaling.mode must be one of none|hpa|http|sqs, got %q" $m) -}}
{{- end -}}
{{- $m -}}
{{- end -}}

{{/* Minimum pod count for the active scaling mode, used to choose the PDB style */}}
{{- define "app.minReplicas" -}}
{{- $m := include "app.mode" . -}}
{{- if eq $m "hpa" -}}{{ .Values.scaling.hpa.min }}
{{- else if eq $m "http" -}}{{ .Values.scaling.http.min }}
{{- else if eq $m "sqs" -}}{{ .Values.scaling.sqs.min }}
{{- else -}}{{ .Values.replicas }}
{{- end -}}
{{- end -}}
