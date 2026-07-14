{{- define "sith-hub.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{- define "sith-hub.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "sith-hub.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "sith-hub.labels" -}}
app.kubernetes.io/name: {{ include "sith-hub.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "sith-hub.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sith-hub.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "sith-hub.imageReference" -}}
{{- $reference := required "image.reference must be an immutable repository@sha256 reference" .Values.image.reference -}}
{{- if not (regexMatch "^[^@[:space:]]+@sha256:[a-f0-9]{64}$" $reference) -}}
{{- fail "image.reference must be an immutable repository@sha256:<64 lowercase hex> reference; image tags are forbidden" -}}
{{- end -}}
{{- $reference -}}
{{- end -}}

{{- define "sith-hub.runtimeSecretName" -}}
{{- required "runtime.existingSecret must name an operator-provided Secret" .Values.runtime.existingSecret -}}
{{- end -}}

{{- define "sith-hub.migrationSecretName" -}}
{{- required "migration.existingSecret must name an operator-provided Secret" .Values.migration.existingSecret -}}
{{- end -}}

{{- define "sith-hub.profile" -}}
{{- if hasKey .Values "resources" -}}
{{- fail "resources is not configurable; select the fixed light or heavy profile" -}}
{{- end -}}
{{- $profile := required "profile must be light or heavy" .Values.profile -}}
{{- if not (has $profile (list "light" "heavy")) -}}
{{- fail "profile must be light or heavy; arbitrary resource profiles are forbidden" -}}
{{- end -}}
{{- $profile -}}
{{- end -}}

{{- define "sith-hub.resources" -}}
{{- $profile := include "sith-hub.profile" . -}}
{{- if eq $profile "light" }}
requests:
  cpu: "100m"
  memory: "128Mi"
limits:
  cpu: "500m"
  memory: "512Mi"
{{- else if eq $profile "heavy" }}
requests:
  cpu: "500m"
  memory: "512Mi"
limits:
  cpu: "2"
  memory: "2Gi"
{{- else -}}
{{- fail "profile must be light or heavy" -}}
{{- end -}}
{{- end -}}
