{{- /*
  REFERENCE ONLY. This file is not part of the chart and nothing in the repository renders or applies it:
  no script, no Helm release and no CI job reads docs/reference/.
  It is the "two ScaledJobs" design of docs/BAO_CAO.md (sections 3.6, 5.2 and 6.3), kept so the design can be read
  and compared with charts/app/templates/scaledjob-sqs.yaml. It replaces that file in the comparison, nothing else.
  It reads the values under scaling.sqsjob.split.*, which only worker.split.values.yaml defines.
  It was checked with `helm lint` and `helm template` only (see README.md in this folder) and has not been deployed.
*/}}
{{- if eq (include "app.mode" .) "sqsjob" }}
{{- /* ScaledJob: KEDA starts one Kubernetes Job per batch of queue messages. Each Job takes at most
       messagesPerJob messages, processes them and exits (WORKER_MODE=job).
       Default: one ScaledJob with both triggers. With scaling.sqsjob.split.enabled the same triggers are
       spread over two ScaledJobs (reference variant, see values.yaml and docs/BAO_CAO.md section 3.6). */}}
apiVersion: keda.sh/v1alpha1
kind: TriggerAuthentication
metadata:
  name: {{ include "app.name" . }}-aws
  labels: {{- include "app.labels" . | nindent 4 }}
spec:
  podIdentity:
    provider: aws
{{- if and .Values.scaling.sqsjob.postgres.enabled .Values.scaling.sqsjob.postgres.host }}
{{- /* KEDA's operator reads the database password from Secrets Manager with its own IAM role (Pod Identity). */}}
---
apiVersion: keda.sh/v1alpha1
kind: TriggerAuthentication
metadata:
  name: {{ include "app.name" . }}-db
  labels: {{- include "app.labels" . | nindent 4 }}
spec:
  awsSecretManager:
    podIdentity:
      provider: aws
    region: {{ .Values.scaling.sqsjob.region | quote }}
    secrets:
      - parameter: userName
        name: {{ required "scaling.sqsjob.postgres.secretARN is required" .Values.scaling.sqsjob.postgres.secretARN | quote }}
        secretKey: username
      - parameter: password
        name: {{ .Values.scaling.sqsjob.postgres.secretARN | quote }}
        secretKey: password
{{- end }}
{{- /* One entry per ScaledJob. "sqs" and "pg" say which triggers that ScaledJob carries. The split needs the
       database trigger; without it the chart keeps the single ScaledJob. */}}
{{- $sq := .Values.scaling.sqsjob }}
{{- $hasPg := and $sq.postgres.enabled $sq.postgres.host }}
{{- $split := and $sq.split.enabled $hasPg }}
{{- $variants := list (dict "suffix" "" "poll" $sq.pollingInterval "max" $sq.max "sqs" true "pg" $hasPg) }}
{{- if $split }}
{{- $variants = list (dict "suffix" "" "poll" $sq.pollingInterval "max" $sq.max "sqs" false "pg" true) (dict "suffix" "-sqs" "poll" $sq.split.safetyPollingInterval "max" (default $sq.max $sq.split.safetyMax) "sqs" true "pg" false) }}
{{- end }}
---
{{- range $variant := $variants }}
---
apiVersion: keda.sh/v1alpha1
kind: ScaledJob
metadata:
  name: {{ include "app.name" $ }}{{ $variant.suffix }}
  labels: {{- include "app.labels" $ | nindent 4 }}
spec:
  pollingInterval: {{ $variant.poll }}
  minReplicaCount: 0
  maxReplicaCount: {{ $variant.max }}
  successfulJobsHistoryLimit: {{ $sq.successfulJobsHistoryLimit }}
  failedJobsHistoryLimit: {{ $sq.failedJobsHistoryLimit }}
  scalingStrategy:
    {{- if not $split }}
    # With the SQS and the postgresql trigger both on, the larger of the two answers wins.
    multipleScalersCalculation: max
    {{- end }}
    # The SQS scaler counts messages that are already in flight, so the Jobs that hold them must be subtracted
    # ("default"). "accurate" assumes in-flight messages are not counted and would create new Jobs for the same
    # message on every poll until the first Job has picked it up.
    strategy: {{ $sq.strategy | quote }}
    {{- with $sq.pendingPodConditions }}
    # A Job whose pod does not yet meet these conditions counts as pending and is not duplicated.
    pendingPodConditions: {{- toYaml . | nindent 6 }}
    {{- end }}
  jobTargetRef:
    parallelism: 1
    completions: 1
    # A message that fails is not deleted and comes back through SQS, so a failed Job is not retried here.
    backoffLimit: 0
    activeDeadlineSeconds: {{ $sq.activeDeadlineSeconds }}
    ttlSecondsAfterFinished: {{ $sq.ttlSecondsAfterFinished }}
    template:
      metadata:
        labels: {{- include "app.labels" $ | nindent 10 }}
      spec:
        restartPolicy: Never
        terminationGracePeriodSeconds: {{ $.Values.terminationGracePeriodSeconds }}
        {{- with $.Values.serviceAccountName }}
        serviceAccountName: {{ . }}
        {{- end }}
        {{- with $.Values.imagePullSecrets }}
        imagePullSecrets: {{- toYaml . | nindent 10 }}
        {{- end }}
        nodeSelector:
          nodepool: {{ $.Values.nodePool }}
          {{- if ne $.Values.arch "auto" }}
          kubernetes.io/arch: {{ $.Values.arch }}
          {{- end }}
        {{- if eq $.Values.nodePool "worker" }}
        tolerations:
          - key: workload
            operator: Equal
            value: worker
            effect: NoSchedule
        {{- end }}
        containers:
          - name: app
            image: "{{ $.Values.image.repository }}:{{ $.Values.image.tag }}"
            imagePullPolicy: {{ $.Values.image.pullPolicy }}
            {{- with $.Values.command }}
            command: {{- toYaml . | nindent 14 }}
            {{- end }}
            {{- with $.Values.args }}
            args: {{- toYaml . | nindent 14 }}
            {{- end }}
            env:
              - name: POD_NAME
                valueFrom:
                  fieldRef:
                    fieldPath: metadata.name
              - name: NODE_NAME
                valueFrom:
                  fieldRef:
                    fieldPath: spec.nodeName
              - name: WORKER_MODE
                value: "job"
              - name: JOB_BATCH
                value: {{ $sq.messagesPerJob | quote }}
              - name: JOB_WAIT_SECONDS
                value: {{ $sq.receiveWaitSeconds | quote }}
              {{- range $k, $v := $.Values.env }}
              - name: {{ $k }}
                value: {{ $v | quote }}
              {{- end }}
            {{- with $.Values.envFrom }}
            envFrom: {{- toYaml . | nindent 14 }}
            {{- end }}
            {{- with $.Values.securityContext }}
            securityContext: {{- toYaml . | nindent 14 }}
            {{- end }}
            resources: {{- toYaml $.Values.resources | nindent 14 }}
  triggers:
    {{- if $variant.sqs }}
    - type: aws-sqs-queue
      authenticationRef:
        name: {{ include "app.name" $ }}-aws
      metadata:
        queueURL: {{ required "scaling.sqsjob.queueURL is required" $sq.queueURL | quote }}
        queueLength: {{ $sq.messagesPerJob | quote }}
        awsRegion: {{ $sq.region | quote }}
        identityOwner: operator
        scaleOnInFlight: {{ $sq.scaleOnInFlight | quote }}
    {{- end }}
    {{- if $variant.pg }}
    # Queued screenings counted in the database: always fresh, unlike the SQS queue length, which lags by about
    # 6 seconds when it is polled every second. The Job still takes its messages from SQS.
    - type: postgresql
      authenticationRef:
        name: {{ include "app.name" $ }}-db
      metadata:
        host: {{ $sq.postgres.host | quote }}
        port: {{ $sq.postgres.port | quote }}
        dbName: {{ $sq.postgres.dbName | quote }}
        sslmode: {{ $sq.postgres.sslmode | quote }}
        query: {{ $sq.postgres.query | quote }}
        targetQueryValue: {{ $sq.messagesPerJob | quote }}
        activationTargetQueryValue: "0"
    {{- end }}
{{- end }}
{{- end }}
