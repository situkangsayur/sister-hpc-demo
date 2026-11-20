// k8swatch: service Go yang memakai client-go untuk men-deploy aplikasi katalog ke k3s,
// membaca status pod, dan menjalankan aksi demo (hapus pod, scaling, rolling update,
// readinessProbe on/off). Ia juga terus mengirim beban 5 request/detik ke Service NodePort
// supaya dampak setiap aksi terhadap request terlihat (sukses, gagal, pod mana yang menjawab).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/logx"
)

const (
	ns       = "default"
	app      = "katalog"
	nodePort = 30080
)

// Hit adalah hasil satu request beban ke Service.
type Hit struct {
	At      int64  `json:"at_ms"`
	OK      bool   `json:"ok"`
	Pod     string `json:"pod,omitempty"`
	Version string `json:"version,omitempty"`
	Err     string `json:"err,omitempty"`
	Ms      int64  `json:"ms"`
}

// Event adalah catatan aksi dan pengamatan (mis. waktu pulih).
type Event struct {
	At   int64  `json:"at_ms"`
	Kind string `json:"kind"`
	Msg  string `json:"msg"`
}

// PodView adalah ringkasan satu pod untuk UI.
type PodView struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Ready    bool   `json:"ready"`
	Deleting bool   `json:"deleting"`
	Version  string `json:"version"`
	IP       string `json:"ip"`
	Age      int64  `json:"age_s"`
	Restarts int32  `json:"restarts"`
	RS       string `json:"replicaset"`
}

type watcher struct {
	cs      *kubernetes.Clientset
	target  string
	image   string
	log     *slog.Logger
	mu      sync.Mutex
	hits    []Hit
	events  []Event
	ready   bool
	kill    *killMark
	initErr string
}

// killMark mencatat pod yang dihapus untuk menghitung waktu pulih (self-healing).
type killMark struct {
	pod     string
	at      time.Time
	desired int32
}

func (w *watcher) event(kind, msg string) {
	w.mu.Lock()
	w.events = append(w.events, Event{At: time.Now().UnixMilli(), Kind: kind, Msg: msg})
	if len(w.events) > 60 {
		w.events = w.events[len(w.events)-60:]
	}
	w.mu.Unlock()
	w.log.Info("k8s event", "kind", kind, "msg", msg)
}

func probe() *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:     corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(8080)}},
		PeriodSeconds:    1,
		FailureThreshold: 1,
		SuccessThreshold: 1,
	}
}

func (w *watcher) deployment() *appsv1.Deployment {
	replicas := int32(3)
	maxU, maxS := intstr.FromInt32(0), intstr.FromInt32(1)
	labels := map[string]string{"app": app}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: app, Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{MaxUnavailable: &maxU, MaxSurge: &maxS}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: ptr(int64(10)),
					Containers: []corev1.Container{{
						Name: app, Image: w.image, ImagePullPolicy: corev1.PullNever,
						Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
						Env: []corev1.EnvVar{
							{Name: "VERSION", Value: "v1"},
							{Name: "STARTUP_DELAY_MS", Value: "3000"},
							{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
						},
						ReadinessProbe: probe(),
						Resources:      corev1.ResourceRequirements{},
					}},
				},
			},
		},
	}
}

func ptr[T any](v T) *T { return &v }

func (w *watcher) ensure(ctx context.Context) error {
	d := w.cs.AppsV1().Deployments(ns)
	if _, err := d.Get(ctx, app, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		if _, err := d.Create(ctx, w.deployment(), metav1.CreateOptions{}); err != nil {
			return err
		}
		w.event("apply", "Deployment katalog dibuat: 3 replica, readinessProbe /healthz, rolling update maxUnavailable=0 maxSurge=1")
	} else if err != nil {
		return err
	}
	s := w.cs.CoreV1().Services(ns)
	if _, err := s.Get(ctx, app, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: app, Namespace: ns},
			Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, Selector: map[string]string{"app": app},
				Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(8080), NodePort: nodePort}}},
		}
		if _, err := s.Create(ctx, svc, metav1.CreateOptions{}); err != nil {
			return err
		}
		w.event("apply", fmt.Sprintf("Service katalog dibuat (NodePort %d) yang membagi beban ke pod yang Ready", nodePort))
	} else if err != nil {
		return err
	}
	return nil
}

// load mengirim 5 request per detik ke Service, seperti pod "beban" di praktikum buku.
func (w *watcher) load(ctx context.Context) {
	// Tanpa keep-alive: setiap request membuka koneksi baru seperti loop curl di praktikum buku,
	// sehingga kube-proxy membagi request ke semua pod (bukan menempel ke satu koneksi/pod).
	cl := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	warm := false
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		w.mu.Lock()
		ok := w.ready
		w.mu.Unlock()
		if !ok {
			continue
		}
		h := Hit{At: time.Now().UnixMilli()}
		start := time.Now()
		resp, err := cl.Get(w.target + "/item/1")
		h.Ms = time.Since(start).Milliseconds()
		if err != nil {
			h.Err = err.Error()
		} else {
			var body struct {
				Pod     string `json:"pod"`
				Version string `json:"version"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			h.OK = resp.StatusCode == 200
			h.Pod, h.Version = body.Pod, body.Version
			if !h.OK {
				h.Err = "HTTP " + strconv.Itoa(resp.StatusCode)
			}
		}
		// Kegagalan sebelum pod pertama siap (saat deploy awal) tidak dicatat.
		if h.OK {
			warm = true
		}
		if !warm {
			continue
		}
		w.mu.Lock()
		w.hits = append(w.hits, h)
		if len(w.hits) > 600 {
			w.hits = w.hits[len(w.hits)-600:]
		}
		w.mu.Unlock()
		if !h.OK {
			w.log.Warn("request gagal", "err", h.Err)
		}
	}
}

func (w *watcher) pods(ctx context.Context) ([]PodView, error) {
	list, err := w.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app=" + app})
	if err != nil {
		return nil, err
	}
	var out []PodView
	for _, p := range list.Items {
		v := PodView{Name: p.Name, Phase: string(p.Status.Phase), IP: p.Status.PodIP, Deleting: p.DeletionTimestamp != nil,
			Age: int64(time.Since(p.CreationTimestamp.Time).Seconds())}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				v.Ready = true
			}
		}
		for _, c := range p.Spec.Containers {
			for _, e := range c.Env {
				if e.Name == "VERSION" {
					v.Version = e.Value
				}
			}
		}
		for _, cs := range p.Status.ContainerStatuses {
			v.Restarts += cs.RestartCount
		}
		for _, o := range p.OwnerReferences {
			v.RS = o.Name
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// track memeriksa apakah kluster sudah pulih setelah pod dihapus.
func (w *watcher) track(ctx context.Context) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		w.mu.Lock()
		k := w.kill
		w.mu.Unlock()
		if k == nil {
			continue
		}
		pods, err := w.pods(ctx)
		if err != nil {
			continue
		}
		var ready int32
		gone := true
		for _, p := range pods {
			if p.Ready && !p.Deleting {
				ready++
			}
			if p.Name == k.pod {
				gone = false
			}
		}
		if ready >= k.desired && gone {
			w.mu.Lock()
			w.kill = nil
			w.mu.Unlock()
			w.event("pulih", fmt.Sprintf("self-healing selesai: %d/%d pod Ready lagi %.1f detik setelah %s dihapus",
				ready, k.desired, time.Since(k.at).Seconds(), k.pod))
		} else if time.Since(k.at) > 2*time.Minute {
			w.mu.Lock()
			w.kill = nil
			w.mu.Unlock()
		}
	}
}

func (w *watcher) status(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	ready, initErr := w.ready, w.initErr
	hits := append([]Hit(nil), w.hits...)
	events := append([]Event(nil), w.events...)
	w.mu.Unlock()
	out := map[string]any{"ready": ready, "init_error": initErr, "hits": hits, "events": events, "target": w.target}
	if !ready {
		httpx.JSON(rw, 200, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	pods, err := w.pods(ctx)
	if err != nil {
		out["error"] = err.Error()
	}
	out["pods"] = pods
	if d, err := w.cs.AppsV1().Deployments(ns).Get(ctx, app, metav1.GetOptions{}); err == nil {
		version, probeOn := "", d.Spec.Template.Spec.Containers[0].ReadinessProbe != nil
		for _, e := range d.Spec.Template.Spec.Containers[0].Env {
			if e.Name == "VERSION" {
				version = e.Value
			}
		}
		out["deployment"] = map[string]any{"desired": *d.Spec.Replicas, "ready": d.Status.ReadyReplicas,
			"updated": d.Status.UpdatedReplicas, "available": d.Status.AvailableReplicas, "unavailable": d.Status.UnavailableReplicas,
			"version": version, "readiness_probe": probeOn, "generation": d.Generation, "observed": d.Status.ObservedGeneration}
	}
	if rs, err := w.cs.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{LabelSelector: "app=" + app}); err == nil {
		var list []map[string]any
		for _, x := range rs.Items {
			v := ""
			for _, e := range x.Spec.Template.Spec.Containers[0].Env {
				if e.Name == "VERSION" {
					v = e.Value
				}
			}
			list = append(list, map[string]any{"name": x.Name, "version": v, "desired": *x.Spec.Replicas, "ready": x.Status.ReadyReplicas,
				"revision": x.Annotations["deployment.kubernetes.io/revision"]})
		}
		sort.Slice(list, func(i, j int) bool { return fmt.Sprint(list[i]["revision"]) < fmt.Sprint(list[j]["revision"]) })
		out["replicasets"] = list
	}
	if nodes, err := w.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		var nl []map[string]any
		for _, n := range nodes.Items {
			ready := false
			for _, c := range n.Status.Conditions {
				if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
					ready = true
				}
			}
			nl = append(nl, map[string]any{"name": n.Name, "ready": ready, "version": n.Status.NodeInfo.KubeletVersion})
		}
		out["nodes"] = nl
	}
	httpx.JSON(rw, 200, out)
}

func (w *watcher) patch(ctx context.Context, body string) error {
	_, err := w.cs.AppsV1().Deployments(ns).Patch(ctx, app, types.StrategicMergePatchType, []byte(body), metav1.PatchOptions{})
	return err
}

func (w *watcher) action(rw http.ResponseWriter, r *http.Request) {
	var in struct {
		Action   string `json:"action"`
		Pod      string `json:"pod"`
		Replicas int32  `json:"replicas"`
		Version  string `json:"version"`
		Enabled  bool   `json:"enabled"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Err(rw, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var err error
	switch in.Action {
	case "delete-pod":
		pods, perr := w.pods(ctx)
		if perr != nil {
			err = perr
			break
		}
		target := in.Pod
		if target == "" {
			for _, p := range pods {
				if p.Ready && !p.Deleting {
					target = p.Name
					break
				}
			}
		}
		if target == "" {
			err = errors.New("tidak ada pod Ready untuk dihapus")
			break
		}
		d, derr := w.cs.AppsV1().Deployments(ns).Get(ctx, app, metav1.GetOptions{})
		if derr != nil {
			err = derr
			break
		}
		if err = w.cs.CoreV1().Pods(ns).Delete(ctx, target, metav1.DeleteOptions{}); err == nil {
			w.mu.Lock()
			w.kill = &killMark{pod: target, at: time.Now(), desired: *d.Spec.Replicas}
			w.mu.Unlock()
			w.event("hapus", "pod "+target+" dihapus; ReplicaSet controller akan membuat pengganti")
		}
	case "scale":
		if in.Replicas < 1 || in.Replicas > 6 {
			err = errors.New("replicas harus 1..6")
			break
		}
		if err = w.patch(ctx, fmt.Sprintf(`{"spec":{"replicas":%d}}`, in.Replicas)); err == nil {
			w.event("scale", fmt.Sprintf("desired state diubah: replicas=%d", in.Replicas))
		}
	case "rollout":
		if in.Version == "" {
			in.Version = "v2"
		}
		body := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":"%s","env":[{"name":"VERSION","value":"%s"}]}]}}}}`, app, in.Version)
		if err = w.patch(ctx, body); err == nil {
			w.event("rollout", "rolling update ke "+in.Version+": ReplicaSet baru dibuat, pod lama diganti satu per satu")
		}
	case "readiness":
		var body string
		if in.Enabled {
			pb, _ := json.Marshal(probe())
			body = fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":"%s","readinessProbe":%s}]}}}}`, app, pb)
		} else {
			body = fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":"%s","readinessProbe":null}]}}}}`, app)
		}
		if err = w.patch(ctx, body); err == nil {
			w.event("probe", fmt.Sprintf("readinessProbe %s (template berubah, terjadi rolling update)", map[bool]string{true: "dinyalakan", false: "dimatikan"}[in.Enabled]))
		}
	case "clear":
		w.mu.Lock()
		w.hits = nil
		w.mu.Unlock()
	default:
		err = errors.New("action tidak dikenal")
	}
	if err != nil {
		httpx.Err(rw, 400, err.Error())
		return
	}
	httpx.JSON(rw, 200, map[string]string{"status": "ok"})
}

func (w *watcher) init(ctx context.Context, kubeconfig, server string) {
	for i := 0; ; i++ {
		err := func() error {
			if _, err := os.Stat(kubeconfig); err != nil {
				return fmt.Errorf("kubeconfig belum ada (%s); k3s masih start", kubeconfig)
			}
			cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
			if err != nil {
				return err
			}
			// kubeconfig dari k3s menunjuk 127.0.0.1:6443; dari container lain alamatnya nama service.
			cfg.Host = server
			cfg.Timeout = 5 * time.Second
			cs, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				return err
			}
			w.cs = cs
			return w.ensure(ctx)
		}()
		if err == nil {
			w.mu.Lock()
			w.ready, w.initErr = true, ""
			w.mu.Unlock()
			w.log.Info("terhubung ke k3s", "server", server)
			return
		}
		w.mu.Lock()
		w.initErr = err.Error()
		w.mu.Unlock()
		if i%10 == 0 {
			w.log.Warn("menunggu k3s", "err", err.Error())
		}
		time.Sleep(2 * time.Second)
	}
}

func main() {
	log := logx.New("k8s-watch")
	w := &watcher{
		target: httpx.Env("TARGET_URL", fmt.Sprintf("http://k3s:%d", nodePort)),
		image:  httpx.Env("KATALOG_IMAGE", "sister-bab07-katalog:v1"),
		log:    log,
	}
	ctx := context.Background()
	go w.init(ctx, httpx.Env("KUBECONFIG", "/kube/kubeconfig.yaml"), httpx.Env("K3S_SERVER", "https://k3s:6443"))
	go w.load(ctx)
	go w.track(ctx)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", w.status)
	mux.HandleFunc("POST /action", w.action)
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, _ *http.Request) { rw.Write([]byte("ok")) })
	addr := ":" + httpx.Env("PORT", "8080")
	log.Info("mulai", "addr", addr)
	if err := http.ListenAndServe(addr, httpx.Log(log, mux)); err != nil {
		log.Error("berhenti", "err", err.Error())
	}
}
