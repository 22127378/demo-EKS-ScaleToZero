#!/usr/bin/env bash
# Proves the generic chart runs third-party images unchanged: each image is installed with the
# same chart, only image/port/args differ, and is probed over HTTP from inside the cluster.
#
# Usage: scripts/test-plug-image.sh [--keep]
#   --keep   leave the test namespace and releases in place (default: clean up at the end)
#
# Needs: a working kubeconfig, helm, docker (for detect-arch.sh), and the cluster add-ons installed.
set -uo pipefail

keep=0
[[ "${1:-}" == "--keep" ]] && keep=1
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ns=demo-plug

# name | image | tag | port | extra helm args
cases=(
  "nginx|nginx|1.27|80|"
  "echo|hashicorp/http-echo|latest|5678|--set args[0]=-text=hello"
  "amd64only|gcr.io/google-samples/hello-app|1.0|8080|"
)

kubectl get namespace "$ns" >/dev/null 2>&1 || kubectl create namespace "$ns" >/dev/null
fail=0
declare -A archs

echo "== install"
for c in "${cases[@]}"; do
  IFS='|' read -r name image tag port extra <<<"$c"
  arch="$("$root/scripts/detect-arch.sh" "$image:$tag" 2>/dev/null)"
  archs[$name]="$arch"
  # shellcheck disable=SC2086
  if helm upgrade --install "plug-$name" "$root/charts/app" -n "$ns" \
        --set "image.repository=$image" --set "image.tag=$tag" --set "containerPort=$port" \
        --set "arch=$arch" --set ingress.enabled=false --set scaling.mode=none --set replicas=1 \
        --set pdb.enabled=false $extra --wait --timeout 4m >/dev/null 2>&1; then
    node="$(kubectl -n "$ns" get pod -l "app.kubernetes.io/name=plug-$name" -o jsonpath='{.items[0].spec.nodeName}')"
    narch="$(kubectl get node "$node" -o jsonpath='{.metadata.labels.kubernetes\.io/arch}')"
    printf '  [ ok ] %-10s image=%s:%s port=%s detect-arch=%s ran on %s node\n' "$name" "$image" "$tag" "$port" "$arch" "$narch"
  else
    printf '  [FAIL] %-10s image=%s:%s did not become Ready\n' "$name" "$image" "$tag"; fail=1
  fi
done

echo "== HTTP from inside the cluster"
targets=""
for c in "${cases[@]}"; do IFS='|' read -r name _ <<<"$c"; targets+="plug-$name.$ns "; done
out="$(kubectl -n "$ns" run curltest --rm -i --restart=Never --image=curlimages/curl --command -- \
  sh -c "for s in $targets; do code=000; for try in 1 2 3 4 5; do code=\$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://\$s); [ \"\$code\" != 000 ] && break; sleep 2; done; echo \"RESULT \$s \$code\"; done" 2>/dev/null)"
# The freshly started curl pod may need a moment before its network works, hence the retries above.
while read -r tag svc code; do
  [[ "${tag:-}" == RESULT ]] || continue
  if [[ "$code" == 200 ]]; then printf '  [ ok ] %s -> %s\n' "$svc" "$code"; else printf '  [FAIL] %s -> %s\n' "$svc" "${code:-no answer}"; fail=1; fi
done <<<"$out"

echo "== negative control: an amd64-only image forced onto arm64 must NOT run"
helm upgrade --install plug-wrongarch "$root/charts/app" -n "$ns" \
  --set image.repository=gcr.io/google-samples/hello-app --set image.tag=1.0 --set containerPort=8080 \
  --set arch=arm64 --set ingress.enabled=false --set scaling.mode=none --set replicas=1 --set pdb.enabled=false \
  --wait --timeout 90s >/dev/null 2>&1
sleep 5
state="$(kubectl -n "$ns" get pod -l app.kubernetes.io/name=plug-wrongarch -o jsonpath='{.items[0].status.containerStatuses[0].state}' 2>/dev/null)"
reason="$(kubectl -n "$ns" get pod -l app.kubernetes.io/name=plug-wrongarch -o jsonpath='{.items[0].status.containerStatuses[0].state.waiting.reason}{.items[0].status.containerStatuses[0].lastState.terminated.reason}{.items[0].status.containerStatuses[0].lastState.terminated.message}' 2>/dev/null)"
ready="$(kubectl -n "$ns" get pod -l app.kubernetes.io/name=plug-wrongarch -o jsonpath='{.items[0].status.containerStatuses[0].ready}' 2>/dev/null)"
if [[ "$ready" != "true" ]]; then printf '  [ ok ] wrong-arch pod is not Ready (%s)\n' "${reason:-$state}"; else printf '  [FAIL] wrong-arch pod unexpectedly became Ready\n'; fail=1; fi

if (( keep == 0 )); then
  echo "== cleanup"
  kubectl delete namespace "$ns" --wait=false >/dev/null 2>&1
fi
(( fail == 0 )) && echo "RESULT: all checks passed" || echo "RESULT: some checks failed"
exit "$fail"
