{{- define "cc.labels" -}}
app.kubernetes.io/part-of: cluster-config
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
