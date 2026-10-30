package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	saDir          = "/var/run/secrets/kubernetes.io/serviceaccount"
	scaledJobLabel = "scaledjob.keda.sh/name"
	templateTTL    = 30 * time.Second
	wakeTimeout    = 5 * time.Second
)

// waker starts a worker right after a message is queued.
type waker interface {
	Wake(ctx context.Context)
}

// k8sWaker creates a Job straight from the API pod, so the first worker does not wait for KEDA to notice the
// message in SQS (the queue length it reads lags by several seconds). The Job is copied from the ScaledJob's own
// template, so it always matches the worker configuration, and carries the ScaledJob label so KEDA counts it as
// one of its running Jobs and does not start a duplicate. KEDA still scales the worker; this only speeds up the
// start. Any failure is logged and ignored: the message is already in the queue and KEDA will pick it up.
type k8sWaker struct {
	client    *http.Client
	base      string        // e.g. https://10.100.0.1:443
	token     func() string // read per call: bound service account tokens rotate
	namespace string
	scaledJob string
	maxActive int // do not start a Job when this many are already active

	mu       sync.Mutex
	cached   map[string]any
	cachedAt time.Time
}

// newWakerFromEnv returns nil unless WAKE_SCALEDJOB is set and the pod runs inside a cluster.
func newWakerFromEnv() waker {
	name := os.Getenv("WAKE_SCALEDJOB")
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if name == "" || host == "" {
		return nil
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		slog.Warn("wake disabled: no cluster CA", "err", err)
		return nil
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	ns := firstNonEmpty(os.Getenv("WAKE_NAMESPACE"), readTrim(saDir+"/namespace"))
	max := 3
	if v := os.Getenv("WAKE_MAX_ACTIVE"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &max); err != nil || max < 1 {
			max = 3
		}
	}
	return &k8sWaker{
		client:    &http.Client{Timeout: wakeTimeout, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}},
		base:      "https://" + host + ":" + port,
		token:     func() string { return readTrim(saDir + "/token") },
		namespace: ns,
		scaledJob: name,
		maxActive: max,
	}
}

func readTrim(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

func (w *k8sWaker) Wake(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, wakeTimeout)
	defer cancel()
	if err := w.wake(ctx); err != nil {
		slog.Warn("wake worker", "err", err)
	}
}

func (w *k8sWaker) wake(ctx context.Context) error {
	sj, err := w.scaledJobObject(ctx)
	if err != nil {
		return err
	}
	active, err := w.activeJobs(ctx)
	if err != nil {
		return err
	}
	if active >= w.maxActive {
		return nil // enough workers are already running; KEDA handles the rest
	}
	meta, _ := sj["metadata"].(map[string]any)
	spec, _ := sj["spec"].(map[string]any)
	target, ok := spec["jobTargetRef"].(map[string]any)
	if !ok {
		return fmt.Errorf("scaledjob %s has no jobTargetRef", w.scaledJob)
	}
	job := map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"generateName": w.scaledJob + "-",
			"labels":       map[string]any{scaledJobLabel: w.scaledJob, "app.kubernetes.io/created-by": "api-wake"},
			"ownerReferences": []any{map[string]any{
				"apiVersion": "keda.sh/v1alpha1", "kind": "ScaledJob", "name": w.scaledJob, "uid": meta["uid"],
			}},
		},
		"spec": target,
	}
	return w.do(ctx, http.MethodPost, "/apis/batch/v1/namespaces/"+w.namespace+"/jobs", job, nil)
}

// scaledJobObject returns the ScaledJob, cached briefly so most calls skip the extra round trip.
func (w *k8sWaker) scaledJobObject(ctx context.Context) (map[string]any, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cached != nil && time.Since(w.cachedAt) < templateTTL {
		return w.cached, nil
	}
	var sj map[string]any
	if err := w.do(ctx, http.MethodGet, "/apis/keda.sh/v1alpha1/namespaces/"+w.namespace+"/scaledjobs/"+w.scaledJob, nil, &sj); err != nil {
		return nil, err
	}
	w.cached, w.cachedAt = sj, time.Now()
	return sj, nil
}

// activeJobs counts this ScaledJob's Jobs that have not finished.
func (w *k8sWaker) activeJobs(ctx context.Context) (int, error) {
	var list struct {
		Items []struct {
			Status struct {
				Active         int    `json:"active"`
				CompletionTime string `json:"completionTime"`
				Failed         int    `json:"failed"`
			} `json:"status"`
		} `json:"items"`
	}
	path := "/apis/batch/v1/namespaces/" + w.namespace + "/jobs?labelSelector=" + scaledJobLabel + "%3D" + w.scaledJob
	if err := w.do(ctx, http.MethodGet, path, nil, &list); err != nil {
		return 0, err
	}
	n := 0
	for _, j := range list.Items {
		if j.Status.CompletionTime == "" && j.Status.Failed == 0 {
			n++
		}
	}
	return n, nil
}

func (w *k8sWaker) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, w.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+w.token())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data[:min(len(data), 200)])))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// wakeWorker starts a worker Job in the background so the response to the caller is not delayed.
func (s *server) wakeWorker() {
	if s.waker == nil {
		return
	}
	go s.waker.Wake(context.Background())
}
